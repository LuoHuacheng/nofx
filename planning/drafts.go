package planning

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"nofx/planning/core"
	"time"
)

type Draft struct {
	UserID     string    `gorm:"primaryKey" json:"-"`
	ID         string    `gorm:"primaryKey" json:"id"`
	ExchangeID string    `gorm:"index" json:"exchange_id"`
	Venue      string    `json:"venue"`
	Symbol     string    `gorm:"index" json:"symbol"`
	Status     string    `gorm:"index" json:"status"`
	Body       string    `gorm:"type:text" json:"-"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Draft) TableName() string { return "planning_drafts" }

type DraftStore struct {
	DB                        *gorm.DB
	UserID, ExchangeID, Venue string
}

func (d *DraftStore) Records() map[string]core.Record {
	out := map[string]core.Record{}
	var rows []Draft
	if d.DB.Where("user_id = ?", d.UserID).Order("updated_at desc").Limit(200).Find(&rows).Error != nil {
		return out
	}
	for _, row := range rows {
		var p core.Plan
		if json.Unmarshal([]byte(row.Body), &p) == nil {
			out[p.ID] = core.Record{Plan: p, State: row.Status, Updated: row.UpdatedAt}
		}
	}
	return out
}
func (d *DraftStore) Add(plans []core.Plan) error {
	return d.DB.Transaction(func(tx *gorm.DB) error { return d.add(tx, plans) })
}
func (d *DraftStore) add(tx *gorm.DB, plans []core.Plan) error {
	for _, p := range plans {
		if e := p.Verify(time.Now().UTC()); e != nil {
			return e
		}
		if p.Snapshot.Venue != d.Venue || p.Snapshot.Kind != "perpetual" {
			return errors.New("plan must match the connected perpetual account")
		}
		raw, e := json.Marshal(p)
		if e != nil {
			return e
		}
		r := Draft{UserID: d.UserID, ID: p.ID, ExchangeID: d.ExchangeID, Venue: d.Venue, Symbol: p.Snapshot.Symbol, Status: "draft", Body: string(raw), UpdatedAt: time.Now().UTC()}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; e != nil {
			return e
		}
	}
	return nil
}
func (d *DraftStore) Revise(old string, plans []core.Plan, now time.Time) error {
	return d.DB.Transaction(func(tx *gorm.DB) error {
		var row Draft
		if e := tx.Where("user_id = ? AND id = ?", d.UserID, old).First(&row).Error; e != nil {
			return e
		}
		if row.Status != "draft" {
			return errors.New("locked live plan cannot be revised; cancel approval first")
		}
		if e := d.add(tx, plans); e != nil {
			return e
		}
		res := tx.Model(&Draft{}).Where("user_id = ? AND id = ? AND status = ?", d.UserID, old, "draft").Updates(map[string]any{"status": "cancelled", "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("plan changed concurrently")
		}
		return nil
	})
}
