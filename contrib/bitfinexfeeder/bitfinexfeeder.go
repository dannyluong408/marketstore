package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
	"flag"
	"regexp"
	"strings"

	"github.com/alpacahq/marketstore/executor"
	"github.com/alpacahq/marketstore/planner"
	"github.com/alpacahq/marketstore/plugins/bgworker"
	"github.com/alpacahq/marketstore/utils"
	"github.com/alpacahq/marketstore/utils/io"
	"github.com/golang/glog"
	bitfinexv1 "github.com/bitfinexcom/bitfinex-api-go/v1"
	bitfinex "github.com/dannyluong408/bitfinex-api-go/v2"
	"github.com/dannyluong408/bitfinex-api-go/v2/rest"
)

type ByTime []*bitfinex.Candle

func (a ByTime) Len() int           { return len(a) }
func (a ByTime) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByTime) Less(i, j int) bool { return  ConvertMillToTime(a[i].MTS).Before(ConvertMillToTime(a[j].MTS)) }

var (
	api = flag.String("api", "https://api.bitfinex.com/v2/", "v2 REST API URL")
)

var suffixBitfinexDefs = map[string]string{
	"Min": "m",
	"H":   "h",
	"D":   "D",
	"M":   "M",
}

const exchange string = "bitfinex-"

//Convert time from milliseconds to Unix
func ConvertMillToTime(originalTime int64) time.Time {
	i := time.Unix(0, originalTime*int64(time.Millisecond))
	return i
}
// FetchConfig is the configuration for bitfinexFetcher you can define in
// marketstore's config file through bgworker extension.
type FetcherConfig struct {
	// list of currency symbols, defults to ["BTC", "ETH", "LTC", "BCH"]
	Symbols []string `json:"symbols"`
	// time string when to start first time, in "YYYY-MM-DD HH:MM" format
	// if it is restarting, the start is the last written data timestamp
	// otherwise, it starts from an hour ago by default
	QueryStart string `json:"query_start"`
	// such as 5Min, 1D.  defaults to 1Min
	BaseTimeframe string `json:"base_timeframe"`
}

// BitfinexFetcher is the main worker instance.  It implements bgworker.Run().
type BitfinexFetcher struct {
	config        map[string]interface{}
	symbols       []string
	queryStart    time.Time
	baseTimeframe *utils.Timeframe
}

func recast(config map[string]interface{}) *FetcherConfig {
	data, _ := json.Marshal(config)
	ret := FetcherConfig{}
	json.Unmarshal(data, &ret)
	return &ret
}

func GetAllSymbols() []string {

	client := bitfinexv1.NewClient()
	products, err := client.Pairs.All()
	symbols := make([]string, 0)

	if err != nil {
		symbols := []string{"BTCUSD"}
		return symbols
	}else {
		for _, symbol := range products {
			symbols = append(symbols, strings.ToUpper(symbol))
		}
	}
	//fmt.Println("Symbols :", symbols)
	return symbols
}

// NewBgWorker returns the new instance of bitfinexFetcher.  See FetcherConfig
// for the details of available configurations.
func NewBgWorker(conf map[string]interface{}) (bgworker.BgWorker, error) {
	symbols := GetAllSymbols()

	config := recast(conf)
	if len(config.Symbols) > 0 {
		symbols = config.Symbols
	}
	var queryStart time.Time
	if config.QueryStart != "" {
		trials := []string{
			"2006-01-02 03:04:05",
			"2006-01-02T03:04:05",
			"2006-01-02 03:04",
			"2006-01-02T03:04",
			"2006-01-02",
		}
		for _, layout := range trials {
			qs, err := time.Parse(layout, config.QueryStart)
			if err == nil {
				queryStart = qs.In(utils.InstanceConfig.Timezone)
				break
			}
		}
	}
	timeframeStr := "1m"
	if config.BaseTimeframe != "" {
		timeframeStr = config.BaseTimeframe
	}

	return &BitfinexFetcher{
		config:        conf,
		symbols:       symbols,
		queryStart:    queryStart,
		baseTimeframe: utils.NewTimeframe(timeframeStr),
	}, nil
}

func findLastTimestamp(symbol string, tbk *io.TimeBucketKey) time.Time {
	cDir := executor.ThisInstance.CatalogDir
	query := planner.NewQuery(cDir)
	query.AddTargetKey(tbk)
	start := time.Unix(0, 0).In(utils.InstanceConfig.Timezone)
	end := time.Unix(math.MaxInt64, 0).In(utils.InstanceConfig.Timezone)
	query.SetRange(start.Unix(), end.Unix())
	query.SetRowLimit(io.LAST, 1)
	parsed, err := query.Parse()
	if err != nil {
		return time.Time{}
	}
	reader, err := executor.NewReader(parsed)
	csm, _, err := reader.Read()
	cs := csm[*tbk]
	if cs == nil || cs.Len() == 0 {
		return time.Time{}
	}
	ts := cs.GetTime()
	return ts[0]
}

// Run() runs forever to get public historical rate for each configured symbol,
// and writes in marketstore data format.  In case any error including rate limit
// is returned from Bitfinex, it waits for a minute.
func (gd *BitfinexFetcher) Run() {
	symbols := gd.symbols
	client := rest.NewClientWithURL(*api)
	timeStart := time.Time{}

	originalInterval := gd.baseTimeframe.String
	re := regexp.MustCompile("[0-9]+")
	re2 := regexp.MustCompile("[a-zA-Z]+")

	timeIntervalLettersOnly := re.ReplaceAllString(originalInterval, "")
	timeIntervalNumsOnly := re2.ReplaceAllString(originalInterval, "")
	correctIntervalSymbol := suffixBitfinexDefs[timeIntervalLettersOnly]
	//If Interval is formmatted incorrectly
	if len(correctIntervalSymbol) <= 0 {
		glog.Errorf("Interval Symbol Format Incorrect. Setting to time interval to default '1Min'")
		timeIntervalNumsOnly = "1"
		correctIntervalSymbol = "m"
	}

	//Replace interval string with correct one with API call
	timeInterval := timeIntervalNumsOnly + correctIntervalSymbol

	for _, symbol := range symbols {
		tbk := io.NewTimeBucketKey(exchange + symbol + "/" + gd.baseTimeframe.String + "/OHLCV")
		lastTimestamp := findLastTimestamp(exchange + symbol, tbk)
		glog.Infof("lastTimestamp for %s = %v", symbol, lastTimestamp)
		if timeStart.IsZero() || (!lastTimestamp.IsZero() && lastTimestamp.Before(timeStart)) {
			timeStart = lastTimestamp
		}
	}
	if timeStart.IsZero() {
		if !gd.queryStart.IsZero() {
			timeStart = gd.queryStart
		} else {
			timeStart = time.Now().UTC().Add(-time.Hour)
		}
	}
	for {
		timeEnd := timeStart.Add(gd.baseTimeframe.Duration * 300)
		lastTime := timeStart


		var timeStartM int64
		var timeEndM int64

		timeStartM = timeStart.UnixNano() / (int64(time.Millisecond) / int64(time.Nanosecond))
		timeEndM = timeEnd.UnixNano() / (int64(time.Millisecond) / int64(time.Nanosecond))

		for _, symbol := range symbols {

			//Granularity: int(gd.baseTimeframe.Duration.Seconds()),

			glog.Infof("Requesting %s %v - %v", symbol, timeStart, timeEnd)
			rates, err := client.Candles.GetOHLCV(timeInterval, symbol, timeStartM, timeEndM)
			if err != nil {
				glog.Errorf("Response error: %v", err)
				// including rate limit case
				time.Sleep(time.Minute)
				continue
			}
			if len(rates) == 0 {
				glog.Info("len(rates) == 0")
				continue
			}
			epoch := make([]int64, 0)
			open := make([]float64, 0)
			high := make([]float64, 0)
			low := make([]float64, 0)
			close := make([]float64, 0)
			volume := make([]float64, 0)

			sort.Sort(ByTime(rates))

			for _, rate := range rates {
				if ConvertMillToTime(rate.MTS).After(lastTime) {
					lastTime = ConvertMillToTime(rate.MTS)
				}
				epoch = append(epoch, int64(rate.MTS))
				open = append(open, float64(rate.Open))
				high = append(high, float64(rate.High))
				low = append(low, float64(rate.Low))
				close = append(close, float64(rate.Close))
				volume = append(volume, rate.Volume)
			}
			cs := io.NewColumnSeries()
			cs.AddColumn("Epoch", epoch)
			cs.AddColumn("Open", open)
			cs.AddColumn("High", high)
			cs.AddColumn("Low", low)
			cs.AddColumn("Close", close)
			cs.AddColumn("Volume", volume)
			glog.Infof("%s: %d rates between %v - %v", symbol, len(rates),
				ConvertMillToTime(rates[0].MTS), ConvertMillToTime(rates[(len(rates))-1].MTS))
			csm := io.NewColumnSeriesMap()
			tbk := io.NewTimeBucketKey(exchange + symbol + "/" + gd.baseTimeframe.String + "/OHLCV")
			csm.AddColumnSeries(*tbk, cs)
			executor.WriteCSM(csm, false)
		}
		// next fetch start point
		timeStart = lastTime.Add(gd.baseTimeframe.Duration)
		// for the next bar to complete, add it once more
		nextExpected := timeStart.Add(gd.baseTimeframe.Duration)
		now := time.Now()
		toSleep := nextExpected.Sub(now)
		glog.Infof("next expected(%v) - now(%v) = %v", nextExpected, now, toSleep)
		if toSleep > 0 {
			glog.Infof("Sleep for %v", toSleep)
			time.Sleep(toSleep)
		} else if time.Now().Sub(lastTime) < time.Hour {
			// let's not go too fast if the catch up is less than an hour
			time.Sleep(time.Second)
		}
	}
}

func main() {

	client := rest.NewClientWithURL(*api)

	var symbols []string

	symbols = GetAllSymbols()
	fmt.Println("Symbols :", symbols)

	fmt.Println("Testing...")
	timeframe := "1m"
	symbol := "BTCUSD"
	start := int64(516435200000)
	end := int64(1516867200000)

	result, err := client.Candles.GetOHLCV(timeframe, bitfinex.TradingPrefix + symbol, start, end)
	for _, c := range result {
			fmt.Println(c)
	}

	if err != nil {
		fmt.Println("Error!")
	}
}
