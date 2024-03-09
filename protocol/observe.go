package protocol

import (
	"fmt"
)

type Target struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

func (t Target) String() string {
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}
