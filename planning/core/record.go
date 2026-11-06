package core

import "time"

type Record struct {
	Plan    Plan      `json:"plan"`
	State   string    `json:"state"`
	Updated time.Time `json:"updated"`
}
