package planning

import (
	"nofx/planning/harness"
	"time"
)

type Conversation struct {
	UserID     string    `gorm:"primaryKey" json:"-"`
	ID         string    `gorm:"primaryKey" json:"id"`
	ExchangeID string    `json:"exchange_id"`
	ModelID    string    `json:"model_id"`
	Symbol     string    `json:"symbol"`
	Messages   string    `gorm:"type:text" json:"messages"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (Conversation) TableName() string { return "planning_conversations" }

type Preferences struct {
	UserID string `gorm:"primaryKey" json:"-"`
	Text   string `json:"text"`
}

func (Preferences) TableName() string { return "planning_preferences" }

type TurnInput struct {
	harness.Input
	ExchangeID  string `json:"exchange_id"`
	ModelID     string `json:"model_id"`
	Constraints any    `json:"constraints"`
}
