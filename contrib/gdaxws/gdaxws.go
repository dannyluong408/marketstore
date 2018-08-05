package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/alpacahq/marketstore/executor"
	"github.com/alpacahq/marketstore/planner"
	"github.com/alpacahq/marketstore/plugins/bgworker"
	"github.com/alpacahq/marketstore/utils"
	"github.com/alpacahq/marketstore/utils/io"
	"github.com/golang/glog"
	gdax "github.com/preichenberger/go-gdax"
	ws "github.com/gorilla/websocket"
)


const exchange string = "gdax-"

// FetchConfig is the configuration for GdaxWS you can define in
// marketstore's config file through bgworker extension.
type FetcherConfig struct {
	// list of currency symbols, defults to ["BTC", "ETH", "LTC", "BCH"]
	Symbols []string `json:"symbols"`
	// time string when to start first time, in "YYYY-MM-DD HH:MM" format
	// if it is restarting, the start is the last written data timestamp
	// otherwise, it starts from an hour ago by default
	//QueryStart string `json:"query_start"`
	// such as 5Min, 1D.  defaults to 1Min
	//BaseTimeframe string `json:"base_timeframe"`
}

// GdaxWS is the main worker instance.  It implements bgworker.Run().
type GdaxWS struct {
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
	client := gdax.NewClient("", "", "")
	products, err := client.GetProducts()
	symbols := make([]string, 0)

	if err != nil {
		symbols := []string{"BCH-BTC", "BCH-USD", "BTC-EUR", "BTC-GBP", "BTC-USD", "ETH-BTC",
			"ETH-EUR", "ETH-USD", "LTC-BTC", "LTC-EUR", "LTC-USD", "BCH-EUR"}
		return symbols
	} else {
		for _, symbol := range products {
			symbols = append(symbols, symbol.Id)
		}
	}
	return symbols
}

// NewBgWorker returns the new instance of GdaxWS.  See FetcherConfig
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
	timeframeStr := "1Min"
	if config.BaseTimeframe != "" {
		timeframeStr = config.BaseTimeframe
	}
	return &GdaxWS{
		config:        conf,
		symbols:       symbols,
		queryStart:    queryStart,
		baseTimeframe: utils.NewTimeframe(timeframeStr),
	}, nil
}

// func findLastTimestamp(symbol string, tbk *io.TimeBucketKey) time.Time {
// 	cDir := executor.ThisInstance.CatalogDir
// 	query := planner.NewQuery(cDir)
// 	query.AddTargetKey(tbk)
// 	start := time.Unix(0, 0).In(utils.InstanceConfig.Timezone)
// 	end := time.Unix(math.MaxInt64, 0).In(utils.InstanceConfig.Timezone)
// 	query.SetRange(start.Unix(), end.Unix())
// 	query.SetRowLimit(io.LAST, 1)
// 	parsed, err := query.Parse()
// 	if err != nil {
// 		return time.Time{}
// 	}
// 	reader, err := executor.NewReader(parsed)
// 	csm, _, err := reader.Read()
// 	cs := csm[*tbk]
// 	if cs == nil || cs.Len() == 0 {
// 		return time.Time{}
// 	}
// 	ts := cs.GetTime()
// 	return ts[0]
// }

// Run() runs forever to get public historical rate for each configured symbol,
// and writes in marketstore data format.  In case any error including rate limit
// is returned from GDAX, it waits for a minute.
func (gd *GdaxWS) Run() {
	symbols := gd.symbols
	var wsDialer ws.Dialer
	var channels []gdax.MessageChannel

	for _, symbol := range symbols {
		tbk := io.NewTimeBucketKey(exchange + symbol + "/" + gd.baseTimeframe.String + "/OHLCV")
		tickerChannel := gdax.MessageChannel{
			gdax.MessageChannel{
	      Name: "heartbeat",
	      ProductIds: []string{
	        symbol,
	      },
		}
		append(channels, tickerChannel)
	}

	wsConn, _, err := wsDialer.Dial("wss://ws-feed.pro.coinbase.com", nil)
	if err != nil {
	  println(err.Error())
	}

	subscribe := gdax.Message{
	  Type:      "subscribe",
	  Channels: []gdax.MessageChannel{
	    gdax.MessageChannel{
	      Name: "heartbeat",
	      ProductIds: []string{
	        "BTC-USD",
	      },
	    },
	    gdax.MessageChannel{
	      Name: "level2",
	      ProductIds: []string{
	        "BTC-USD",
	      },
	    },
	  },
	}
	if err := wsConn.WriteJSON(subscribe); err != nil {
	  println(err.Error())
	}

  for true {
    message := gdax.Message{}
    if err := wsConn.ReadJSON(&message); err != nil {
      println(err.Error())
      break
    }

    if message.Type == "match" {
      println("Got a match")
    }
  }

	for {
		timeEnd := timeStart.Add(gd.baseTimeframe.Duration * 300)

		lastTime := timeStart

		for _, symbol := range symbols {
			params := gdax.GetHistoricRatesParams{
				Start:       timeStart,
				End:         timeEnd,
				Granularity: int(gd.baseTimeframe.Duration.Seconds()),
			}

			glog.Infof("Requesting %s %v - %v", symbol, timeStart, timeEnd)
			rates, err := client.GetHistoricRates(symbol, params)

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
				if rate.Time.After(lastTime) {
					lastTime = rate.Time
				}
				epoch = append(epoch, rate.Time.Unix())
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
				rates[0].Time, rates[(len(rates))-1].Time)
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
	symbols := GetAllSymbols()
	var wsDialer ws.Dialer
	var channels []gdax.MessageChannel

	for _, symbol := range symbols {
		tbk := io.NewTimeBucketKey(exchange + symbol + "/" + gd.baseTimeframe.String + "/OHLCV")
		tickerChannel := gdax.MessageChannel{
			gdax.MessageChannel{
				Name: "heartbeat",
				ProductIds: []string{
					symbol,
				},
		}
		append(channels, tickerChannel)
	}

	wsConn, _, err := wsDialer.Dial("wss://ws-feed.pro.coinbase.com", nil)
	if err != nil {
		println(err.Error())
	}

	subscribe := gdax.Message{
		Type:      "subscribe",
		Channels: []gdax.MessageChannel{
			gdax.MessageChannel{
				Name: "heartbeat",
				ProductIds: []string{
					"BTC-USD",
				},
			},
			gdax.MessageChannel{
				Name: "level2",
				ProductIds: []string{
					"BTC-USD",
				},
			},
		},
	}
	if err := wsConn.WriteJSON(subscribe); err != nil {
		println(err.Error())
	}

	for true {
		message := gdax.Message{}
		if err := wsConn.ReadJSON(&message); err != nil {
			println(err.Error())
			break
		}

		if message.Type == "match" {
			println("Got a match")
		}
	}
}
