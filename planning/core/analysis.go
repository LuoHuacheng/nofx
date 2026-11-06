package core

import (
	"encoding/hex"
	"errors"
)

type DataWindow struct {
	Interval    string `json:"interval"`
	TargetCount int    `json:"target_count,omitempty"`
	From        int64  `json:"from"`
	To          int64  `json:"to"`
	Count       int    `json:"count"`
	Sampled     bool   `json:"sampled,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}
type Analysis struct {
	HistoryPolicy string       `json:"history_policy,omitempty"`
	PluginID      string       `json:"plugin_id,omitempty"`
	PluginHash    string       `json:"plugin_hash,omitempty"`
	Interval      string       `json:"interval"`
	VisibleFrom   int64        `json:"visible_from,omitempty"`
	VisibleTo     int64        `json:"visible_to,omitempty"`
	Windows       []DataWindow `json:"windows"`
}

func knownInterval(s string) bool {
	return s == "1m" || s == "5m" || s == "15m" || s == "1h" || s == "4h" || s == "1d"
}
func (p Plan) WithAnalysis(a *Analysis) (Plan, error) {
	if a == nil {
		return p, nil
	}
	if (a.HistoryPolicy != "" && a.HistoryPolicy != "latest_history") || !knownInterval(a.Interval) || len(a.Windows) > 16 || a.VisibleFrom < 0 || a.VisibleTo < 0 {
		return Plan{}, errors.New("invalid analysis provenance")
	}
	if (a.PluginID == "") != (a.PluginHash == "") || len(a.PluginID) > 40 {
		return Plan{}, errors.New("invalid plugin provenance")
	}
	if a.PluginHash != "" {
		if b, err := hex.DecodeString(a.PluginHash); err != nil || len(b) != 32 {
			return Plan{}, errors.New("invalid plugin version hash")
		}
	}
	copyAnalysis := *a
	copyAnalysis.Windows = append([]DataWindow(nil), a.Windows...)
	for _, w := range copyAnalysis.Windows {
		if !knownInterval(w.Interval) || w.Count < 0 || w.Count > 1200 || w.TargetCount < 0 || w.TargetCount > 1200 || w.From < 0 || w.To < w.From || w.To > p.Created.Unix()+30 {
			return Plan{}, errors.New("invalid analysis window")
		}
	}
	p.Analysis = &copyAnalysis
	p.Hash = p.Digest()
	p.ID = p.Hash[:16]
	return p, nil
}
