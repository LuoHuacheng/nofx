package plugins

// Visual is a bounded, typed presentation DTO. No HTML, scripts or URLs are executed.
type Visual struct {
	Kind        string        `json:"kind"`
	State       string        `json:"state"`
	Coverage    string        `json:"coverage"`
	DataAt      string        `json:"data_at"`
	WindowStart string        `json:"window_start,omitempty"`
	Mark        float64       `json:"mark,omitempty"`
	Bins        []CostBin     `json:"bins,omitempty"`
	Cells       []HeatCell    `json:"cells,omitempty"`
	Rows        int           `json:"rows,omitempty"`
	Columns     int           `json:"columns,omitempty"`
	WinMin      float64       `json:"win_min"`
	WinMax      float64       `json:"win_max"`
	CostMin     float64       `json:"cost_min"`
	CostMax     float64       `json:"cost_max"`
	Entries     []ProfitEntry `json:"entries,omitempty"`
}
type CostBin struct {
	Px        float64 `json:"px"`
	LongCost  float64 `json:"longCost"`
	ShortCost float64 `json:"shortCost"`
	LongLiq   float64 `json:"longLiq"`
	ShortLiq  float64 `json:"shortLiq"`
}
type HeatTotal struct {
	Count    int    `json:"count"`
	Notional string `json:"notional"`
}
type HeatCell struct {
	Row    int       `json:"row"`
	Column int       `json:"column"`
	Long   HeatTotal `json:"long"`
	Short  HeatTotal `json:"short"`
}
type ProfitEntry struct {
	Rank     int    `json:"rank"`
	Address  string `json:"address"`
	Net      string `json:"netPnl"`
	Realized string `json:"realizedPnl"`
	Fees     string `json:"fees"`
	Funding  string `json:"funding"`
	Volume   string `json:"volume"`
}

// ModelContext deliberately excludes the visualization's address/point arrays.
func ModelContext(r Result) Result { r.Visual = nil; return r }
