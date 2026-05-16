package ovh

import (
	"fmt"
	"sync"

	"github.com/ovh/go-ovh/ovh"

	"github.com/ovh-buy/server/internal/config"
)

// Factory 按当前 config 动态创建 OVH client（每次调用都用最新凭据）
type Factory struct {
	cfg *config.Store
	mu  sync.Mutex
}

// NewFactory 创建工厂
func NewFactory(cfg *config.Store) *Factory {
	return &Factory{cfg: cfg}
}

// Client 返回一个新的 OVH client；凭据不全时返回 nil + error
func (f *Factory) Client() (*ovh.Client, error) {
	c := f.cfg.Get()
	if c.AppKey == "" || c.AppSecret == "" || c.ConsumerKey == "" {
		return nil, fmt.Errorf("missing OVH API credentials")
	}
	cli, err := ovh.NewClient(c.Endpoint, c.AppKey, c.AppSecret, c.ConsumerKey)
	if err != nil {
		return nil, err
	}
	return cli, nil
}
