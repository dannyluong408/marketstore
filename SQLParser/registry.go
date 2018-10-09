package SQLParser

import (
	"github.com/dannyluong408/marketstore/contrib/candler/candlecandler"
	"github.com/dannyluong408/marketstore/contrib/candler/tickcandler"
	"github.com/dannyluong408/marketstore/uda"
	"github.com/dannyluong408/marketstore/uda/avg"
	"github.com/dannyluong408/marketstore/uda/count"
	"github.com/dannyluong408/marketstore/uda/max"
	"github.com/dannyluong408/marketstore/uda/min"
)

var AggRegistry = map[string]uda.AggInterface{
	"TickCandler":   &tickcandler.TickCandler{},
	"tickcandler":   &tickcandler.TickCandler{},
	"CandleCandler": &candlecandler.CandleCandler{},
	"candlecandler": &candlecandler.CandleCandler{},
	"Count":         &count.Count{},
	"count":         &count.Count{},
	"Min":           &min.Min{},
	"min":           &min.Min{},
	"Max":           &max.Max{},
	"max":           &max.Max{},
	"Avg":           &avg.Avg{},
	"avg":           &avg.Avg{},
}
