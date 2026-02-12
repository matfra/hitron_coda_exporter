package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type loginClient interface {
	Login(ctx context.Context) error
	Logout(ctx context.Context) error
}

type modemClient interface {
	loginClient
	cmClient
}

type collector struct {
	ctx    context.Context
	client modemClient
	rc     *routerCollector
	cc     cmCollector
	wc     *wifiCollector

	up prometheus.Gauge

	config        config
	collectRouter bool
	collectWiFi   bool
}

func newCollector(ctx context.Context, conf config) *collector {
	c := &collector{ctx: ctx, config: conf}

	collectRouter := true
	if conf.CollectRouter != nil {
		collectRouter = *conf.CollectRouter
	}

	collectWiFi := true
	if conf.CollectWiFi != nil {
		collectWiFi = *conf.CollectWiFi
	}

	if strings.EqualFold(conf.ModemType, "coda56") {
		if conf.CollectRouter == nil {
			collectRouter = false
		}
		if conf.CollectWiFi == nil {
			collectWiFi = false
		}
	}

	c.collectRouter = collectRouter
	c.collectWiFi = collectWiFi

	if collectRouter {
		rc := newRouterCollector(ctx, c.getRouterClient)
		c.rc = &rc
	}
	c.cc = newCMCollector(ctx, c.getCMClient)
	if collectWiFi {
		wc := newWiFiCollector(ctx, c.getWiFiClient)
		c.wc = &wc
	}

	c.up = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNS,
		Name:      "up",
		Help:      "Whether the device is reachable (1), or not (0)",
	})

	return c
}

func (c *collector) getCMClient() cmClient {
	return c.client
}

func (c *collector) getRouterClient() routerClient {
	if c.client == nil {
		return nil
	}

	rc, _ := c.client.(routerClient)

	return rc
}

func (c *collector) getWiFiClient() wifiClient {
	if c.client == nil {
		return nil
	}

	wc, _ := c.client.(wifiClient)

	return wc
}

// Describe implements Prometheus.Collector.
func (c collector) Describe(ch chan<- *prometheus.Desc) {
	if c.rc != nil {
		c.rc.Describe(ch)
	}
	c.cc.Describe(ch)
	if c.wc != nil {
		c.wc.Describe(ch)
	}

	c.up.Describe(ch)
}

// Collect implements Prometheus.Collector.
func (c *collector) Collect(ch chan<- prometheus.Metric) {
	// Assume the worst...
	c.up.Set(0)
	defer c.up.Collect(ch)

	client, err := newModemClient(c.config)
	if err != nil {
		slog.ErrorContext(c.ctx, "Error creating client", "err", err)
		exporterClientErrors.Inc()

		return
	}

	c.client = client

	err = c.client.Login(c.ctx)
	if err != nil {
		slog.ErrorContext(c.ctx, "Error logging in", "err", err)
		exporterClientErrors.Inc()

		return
	}

	defer c.client.Logout(c.ctx)

	if c.rc != nil {
		c.rc.Collect(ch)
	}
	c.cc.Collect(ch)
	if c.wc != nil {
		c.wc.Collect(ch)
	}

	// collect is deferred
	c.up.Set(1)
}
