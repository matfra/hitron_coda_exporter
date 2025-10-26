package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	hitron "github.com/hairyhenderson/hitron_coda"
)

const (
	modemTypeCODA4680 = "coda4680"
	modemTypeCODA56   = "coda56"
)

func newModemClient(conf config) (modemClient, error) {
	modemType := strings.ToLower(strings.TrimSpace(conf.ModemType))
	switch modemType {
	case "", modemTypeCODA4680:
		return hitron.New(conf.Host, conf.Username, conf.Password)
	case modemTypeCODA56:
		return newCODA56Client(conf)
	default:
		return nil, fmt.Errorf("unsupported modem_type %q", conf.ModemType)
	}
}

type coda56Client struct {
	baseURL *url.URL
	hc      *http.Client
}

func newCODA56Client(conf config) (*coda56Client, error) {
	host := strings.TrimSpace(conf.Host)
	if host == "" {
		host = "192.168.100.1"
	}

	scheme := strings.TrimSpace(conf.Scheme)
	if scheme == "" {
		scheme = "https"
	}

	base, err := url.Parse(fmt.Sprintf("%s://%s/", scheme, host))
	if err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if scheme == "https" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: conf.InsecureSkipVerify}
	}

	client := &http.Client{Transport: transport}

	return &coda56Client{baseURL: base, hc: client}, nil
}

func (c *coda56Client) Login(_ context.Context) error {
	return nil
}

func (c *coda56Client) Logout(_ context.Context) error {
	return nil
}

func (c *coda56Client) CMSysInfo(ctx context.Context) (hitron.CMSysInfo, error) {
	var payload []coda56DocsisWAN
	if err := c.getJSON(ctx, "data/getCmDocsisWan.asp", &payload); err != nil {
		return hitron.CMSysInfo{}, err
	}

	if len(payload) == 0 {
		return hitron.CMSysInfo{}, fmt.Errorf("empty DOCSIS WAN response")
	}

	entry := payload[0]

	info := hitron.CMSysInfo{}
	info.NetworkAccess = entry.NetworkAccess
	info.Configname = entry.Configname
	info.IP = net.ParseIP(entry.CmIPAddress)
	info.SubMask = parseMask(entry.CmNetMask)
	info.GW = net.ParseIP(entry.CmGateway)
	info.Lease = parseLeaseDuration(entry.CmIpLeaseDuration)

	return info, nil
}

func (c *coda56Client) CMDsInfo(ctx context.Context) (hitron.CMDsInfo, error) {
	var payload []coda56DownstreamPort
	if err := c.getJSON(ctx, "data/dsinfo.asp", &payload); err != nil {
		return hitron.CMDsInfo{}, err
	}

	ports := make([]hitron.PortInfo, 0, len(payload))
	for _, p := range payload {
		ports = append(ports, p.toPortInfo())
	}

	return hitron.CMDsInfo{Ports: ports}, nil
}

func (c *coda56Client) CMUsInfo(ctx context.Context) (hitron.CMUsInfo, error) {
	var payload []coda56UpstreamPort
	if err := c.getJSON(ctx, "data/usinfo.asp", &payload); err != nil {
		return hitron.CMUsInfo{}, err
	}

	ports := make([]hitron.PortInfo, 0, len(payload))
	for _, p := range payload {
		ports = append(ports, p.toPortInfo())
	}

	return hitron.CMUsInfo{Ports: ports}, nil
}

func (c *coda56Client) CMUsOfdm(ctx context.Context) (hitron.CMUsOfdm, error) {
	var payload []coda56UsOfdm
	if err := c.getJSON(ctx, "data/usofdminfo.asp", &payload); err != nil {
		return hitron.CMUsOfdm{}, err
	}

	channels := make([]hitron.OFDMAChannel, 0, len(payload))
	for _, ch := range payload {
		channels = append(channels, ch.toOFDMAChannel())
	}

	return hitron.CMUsOfdm{Channels: channels}, nil
}

func (c *coda56Client) CMDsOfdm(ctx context.Context) (hitron.CMDsOfdm, error) {
	var payload []coda56DsOfdm
	if err := c.getJSON(ctx, "data/dsofdminfo.asp", &payload); err != nil {
		return hitron.CMDsOfdm{}, err
	}

	receivers := make([]hitron.OFDMReceiver, 0, len(payload))
	for _, r := range payload {
		receivers = append(receivers, r.toOFDMReceiver())
	}

	return hitron.CMDsOfdm{Receivers: receivers}, nil
}

func (c *coda56Client) CMVersion(ctx context.Context) (hitron.CMVersion, error) {
	model := coda56Model{}
	if err := c.getJSON(ctx, "data/system_model.asp", &model); err != nil {
		return hitron.CMVersion{}, err
	}

	return hitron.CMVersion{
		ModelName:  model.ModelName,
		VendorName: model.VendorName,
	}, nil
}

func (c *coda56Client) RouterSysInfo(context.Context) (hitron.RouterSysInfo, error) {
	return hitron.RouterSysInfo{}, fmt.Errorf("router metrics not supported for CODA56")
}

func (c *coda56Client) RouterLocation(context.Context) (hitron.RouterLocation, error) {
	return hitron.RouterLocation{}, fmt.Errorf("router metrics not supported for CODA56")
}

func (c *coda56Client) WiFiClient(context.Context) (hitron.WiFiClient, error) {
	return hitron.WiFiClient{}, fmt.Errorf("wifi metrics not supported for CODA56")
}

func (c *coda56Client) getJSON(ctx context.Context, path string, out interface{}) error {
	rel, err := url.Parse(path)
	if err != nil {
		return err
	}

	q := rel.Query()
	if _, ok := q["_"]; !ok {
		q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
		rel.RawQuery = q.Encode()
	}

	u := c.baseURL.ResolveReference(rel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return err
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("unexpected status from CODA56", slog.String("url", u.String()), slog.Int("status", resp.StatusCode))
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)

	return dec.Decode(out)
}

type coda56DocsisWAN struct {
	Configname        string `json:"Configname"`
	NetworkAccess     string `json:"NetworkAccess"`
	CmIPAddress       string `json:"CmIpAddress"`
	CmNetMask         string `json:"CmNetMask"`
	CmGateway         string `json:"CmGateway"`
	CmIpLeaseDuration string `json:"CmIpLeaseDuration"`
}

type coda56DownstreamPort struct {
	PortID         string `json:"portId"`
	Frequency      string `json:"frequency"`
	Modulation     string `json:"modulation"`
	SignalStrength string `json:"signalStrength"`
	SNR            string `json:"snr"`
	DsOctets       string `json:"dsoctets"`
	Correcteds     string `json:"correcteds"`
	Uncorrect      string `json:"uncorrect"`
	ChannelID      string `json:"channelId"`
}

func (p coda56DownstreamPort) toPortInfo() hitron.PortInfo {
	modulation := map[string]string{
		"0": "16QAM",
		"1": "64QAM",
		"2": "256QAM",
		"3": "1024QAM",
		"4": "32QAM",
		"5": "128QAM",
		"6": "QPSK",
	}

	freq, _ := strconv.ParseInt(strings.TrimSpace(p.Frequency), 10, 64)
	snr, _ := strconv.ParseFloat(strings.TrimSpace(p.SNR), 64)
	strength, _ := strconv.ParseFloat(strings.TrimSpace(p.SignalStrength), 64)
	octets, _ := strconv.ParseInt(strings.TrimSpace(p.DsOctets), 10, 64)
	corrected, _ := strconv.ParseInt(strings.TrimSpace(p.Correcteds), 10, 64)
	uncorrect, _ := strconv.ParseInt(strings.TrimSpace(p.Uncorrect), 10, 64)

	mod := modulation[strings.TrimSpace(p.Modulation)]
	if mod == "" {
		mod = strings.TrimSpace(p.Modulation)
	}

	return hitron.PortInfo{
		PortID:         p.PortID,
		ChannelID:      p.ChannelID,
		Modulation:     mod,
		SignalStrength: strength,
		Frequency:      freq,
		SNR:            snr,
		DsOctets:       octets,
		Correcteds:     corrected,
		Uncorrect:      uncorrect,
	}
}

type coda56UpstreamPort struct {
	PortID         string `json:"portId"`
	Frequency      string `json:"frequency"`
	Bandwidth      string `json:"bandwidth"`
	Modulation     string `json:"modtype"`
	SignalStrength string `json:"signalStrength"`
	ChannelID      string `json:"channelId"`
}

func (p coda56UpstreamPort) toPortInfo() hitron.PortInfo {
	freq, _ := strconv.ParseInt(strings.TrimSpace(p.Frequency), 10, 64)
	bandwidth, _ := strconv.ParseInt(strings.TrimSpace(p.Bandwidth), 10, 64)
	strength, _ := strconv.ParseFloat(strings.TrimSpace(p.SignalStrength), 64)

	modulation := strings.TrimSpace(p.Modulation)
	if modulation == "" {
		modulation = "Unknown"
	}

	return hitron.PortInfo{
		PortID:         p.PortID,
		ChannelID:      strings.TrimSpace(p.ChannelID),
		Modulation:     modulation,
		SignalStrength: strength,
		Frequency:      freq,
		Bandwidth:      bandwidth,
	}
}

type coda56DsOfdm struct {
	Receive        int    `json:"receive"`
	FFTType        string `json:"ffttype"`
	SubcarrierFreq string `json:"Subcarr0freqFreq"`
	PLCLock        string `json:"plclock"`
	NCPLock        string `json:"ncplock"`
	MDC1Lock       string `json:"mdc1lock"`
	PLCPower       string `json:"plcpower"`
}

func (r coda56DsOfdm) toOFDMReceiver() hitron.OFDMReceiver {
	freq, _ := strconv.ParseInt(strings.TrimSpace(r.SubcarrierFreq), 10, 64)
	power, _ := strconv.ParseFloat(strings.TrimSpace(r.PLCPower), 64)

	return hitron.OFDMReceiver{
		ID:             r.Receive,
		FFTType:        strings.TrimSpace(r.FFTType),
		SubcarrierFreq: freq,
		PLCPower:       power,
		PLCLocked:      strings.EqualFold(strings.TrimSpace(r.PLCLock), "YES"),
		NCPLocked:      strings.EqualFold(strings.TrimSpace(r.NCPLock), "YES"),
		MDC1Locked:     strings.EqualFold(strings.TrimSpace(r.MDC1Lock), "YES"),
	}
}

type coda56UsOfdm struct {
	ChannelIndex string `json:"uschindex"`
	State        string `json:"state"`
	Frequency    string `json:"frequency"`
	DigAtten     string `json:"digAtten"`
	DigAttenBo   string `json:"digAttenBo"`
	ChannelBw    string `json:"channelBw"`
	RepPower     string `json:"repPower"`
	RepPower1_6  string `json:"repPower1_6"`
	FFTVal       string `json:"fftVal"`
}

func (c coda56UsOfdm) toOFDMAChannel() hitron.OFDMAChannel {
	id, _ := strconv.Atoi(strings.TrimSpace(c.ChannelIndex))
	freq, _ := strconv.ParseFloat(strings.TrimSpace(c.Frequency), 64)
	_ = freq // unused but ensures parsing occurs if needed in future
	digAtten, _ := strconv.ParseFloat(strings.TrimSpace(c.DigAtten), 64)
	digAttenBo, _ := strconv.ParseFloat(strings.TrimSpace(c.DigAttenBo), 64)
	channelBw, _ := strconv.ParseFloat(strings.TrimSpace(c.ChannelBw), 64)
	repPower, _ := strconv.ParseFloat(strings.TrimSpace(c.RepPower), 64)
	repPower16, _ := strconv.ParseFloat(strings.TrimSpace(c.RepPower1_6), 64)

	enabled := strings.EqualFold(strings.TrimSpace(c.State), "OPERATE") || strings.EqualFold(strings.TrimSpace(c.State), "ENABLED")

	return hitron.OFDMAChannel{
		ID:          id,
		Enable:      enabled,
		FFTSize:     strings.TrimSpace(c.FFTVal),
		DigAtten:    digAtten,
		DigAttenBo:  digAttenBo,
		ChannelBw:   channelBw,
		RepPower:    repPower,
		RepPower1_6: repPower16,
	}
}

type coda56Model struct {
	ModelName  string `json:"modelName"`
	VendorName string `json:"vendorname"`
}

func parseMask(mask string) net.IPMask {
	ip := net.ParseIP(strings.TrimSpace(mask))
	if ip == nil {
		return nil
	}

	if v4 := ip.To4(); v4 != nil {
		return net.IPMask(v4)
	}

	return net.IPMask(ip)
}

func parseLeaseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	fields := strings.Fields(s)
	if len(fields)%2 != 0 {
		return 0
	}

	var days, hours, mins, secs int

	for i := 0; i < len(fields); i += 2 {
		label := strings.TrimSuffix(fields[i], ":")
		value, err := strconv.Atoi(fields[i+1])
		if err != nil {
			continue
		}

		switch label {
		case "D":
			days = value
		case "H":
			hours = value
		case "M":
			mins = value
		case "S":
			secs = value
		}
	}

	return (time.Duration(days) * 24 * time.Hour) +
		(time.Duration(hours) * time.Hour) +
		(time.Duration(mins) * time.Minute) +
		(time.Duration(secs) * time.Second)
}
