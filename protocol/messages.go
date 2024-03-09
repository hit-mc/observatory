package protocol

import (
	"encoding/json"
	"time"
)

type TimeStamp time.Time

func (t *TimeStamp) UnmarshalJSON(data []byte) error {
	var ts int64
	err := json.Unmarshal(data, &ts)
	if err != nil {
		return err
	}
	*t = TimeStamp(time.UnixMilli(ts))
	return nil
}

func (t TimeStamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UnixMilli())
}

type Observation struct {
	Time    TimeStamp `json:"time"`
	Target  Target    `json:"target"`
	Online  bool      `json:"online"`
	Latency uint32    `json:"latency"`
}
