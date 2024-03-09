package config

import (
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"os"
)

type Server struct {
	Listen           string   `toml:"listen"`
	HandshakeTimeout int      `toml:"handshake_timeout"`
	Token            string   `toml:"token"`
	Targets          []Target `toml:"targets"`
}

type Client struct {
	ObserverID           string `toml:"observer_id"`
	ReportServer         string `toml:"report_server"`
	CheckInterval        int    `toml:"check_interval"`
	SendBuffer           int    `toml:"send_buffer"`
	ReconnectInterval    int    `toml:"reconnect_interval"`
	ReportConnectTimeout int    `toml:"report_connect_timeout"`
	Token                string `toml:"token"`
}

type Target struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

func Read[T any](path string) (ret *T, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()
	dec := toml.NewDecoder(f)
	ret = new(T)
	err = dec.Decode(ret)
	return ret, err
}
