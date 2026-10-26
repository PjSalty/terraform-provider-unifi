package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// drMAC is from the RFC 7042 documentation range.
const (
	drMAC      = "00:00:5e:00:53:01"
	drDeviceID = "65f0c0ffee0000000000abcd"
)

// drAP stands in for one AP behind the controller's stat/device (GET) and
// rest/device (PUT) endpoints. A PUT replaces the whole radio table, exactly
// like the controller, so lost updates between radios show up here too.
type drAP struct {
	mu       sync.Mutex
	missing  bool
	table    []map[string]any
	getErr   error
	getErrAt int // fail only the nth GET (1-based); 0 fails every GET once getErr is set
	getDelay time.Duration
	putErr   error
	afterPut func([]map[string]any) []map[string]any
	gets     int
	putPaths []string
	// staleGets makes the next n GETs after each PUT still return the table
	// from before it, as the controller does for a few seconds.
	staleGets int
	stale     []map[string]any
	staleLeft int
}

func (a *drAP) get(_ context.Context, apiPath string, _ any, respBody any) error {
	a.mu.Lock()
	a.gets++
	n := a.gets
	a.mu.Unlock()
	time.Sleep(a.getDelay)
	if a.getErr != nil && (a.getErrAt == 0 || a.getErrAt == n) {
		return a.getErr
	}
	if apiPath != "s/default/stat/device/"+drMAC {
		return fmt.Errorf("unexpected GET %s", apiPath)
	}
	a.mu.Lock()
	table := a.table
	if a.staleLeft > 0 {
		table = a.stale
		a.staleLeft--
	}
	data := []any{}
	if !a.missing {
		data = append(data, map[string]any{"_id": drDeviceID, "mac": drMAC, "radio_table": table})
	}
	b, err := json.Marshal(map[string]any{"data": data})
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return json.Unmarshal(b, respBody)
}

func (a *drAP) put(_ context.Context, apiPath string, reqBody any, respBody any) error {
	if a.putErr != nil {
		return a.putErr
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	var body struct {
		RadioTable []map[string]any `json:"radio_table"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return err
	}
	a.mu.Lock()
	a.putPaths = append(a.putPaths, apiPath)
	a.stale, a.staleLeft = a.table, a.staleGets
	a.table = body.RadioTable
	if a.afterPut != nil {
		a.table = a.afterPut(a.table)
	}
	a.mu.Unlock()
	return json.Unmarshal([]byte(`{"data":[]}`), respBody)
}

func (a *drAP) client() *unifi.ClientMock {
	return &unifi.ClientMock{GetFunc: a.get, PutFunc: a.put}
}

func (a *drAP) entry(code string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return radioEntry(a.table, code)
}

// drTable mixes number and string encodings the way a real controller does.
func drTable() []map[string]any {
	return []map[string]any{
		{"radio": "ng", "name": "wifi0", "channel": json.Number("6"), "ht": json.Number("20"), "tx_power_mode": "auto", "vwire_enabled": true},
		{"radio": "na", "name": "wifi1", "channel": "149", "ht": "40", "tx_power_mode": "high", "tx_power": "auto", "min_rssi_enabled": false, "future_field": "kept"},
		{"radio": "6e", "name": "wifi2", "channel": "auto", "ht": json.Number("160"), "tx_power_mode": "auto", "min_rssi_enabled": true, "min_rssi": json.Number("-80")},
	}
}

func drRes(t *testing.T, c unifi.Client, opts ...testutil.Opt) *deviceRadioResource {
	t.Helper()
	d := &providerdata.Data{Client: c, SiteID: testutil.SiteID, Site: "default"}
	for _, o := range opts {
		o(d)
	}
	r := NewDeviceRadioResource().(*deviceRadioResource)
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: d}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", resp.Diagnostics)
	}
	return r
}

func drSchema(t *testing.T) tfsdk.State {
	t.Helper()
	var resp resource.SchemaResponse
	NewDeviceRadioResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	s := tfsdk.State{Schema: resp.Schema}
	s.RemoveResource(context.Background())
	return s
}

func drPlan(t *testing.T, m deviceRadioModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan(drSchema(t))
	if d := p.Set(context.Background(), m); d.HasError() {
		t.Fatalf("plan set: %v", d)
	}
	return p
}

func drState(t *testing.T, m deviceRadioModel) tfsdk.State {
	t.Helper()
	s := drSchema(t)
	if d := s.Set(context.Background(), m); d.HasError() {
		t.Fatalf("state set: %v", d)
	}
	return s
}

func drJunk(t *testing.T) (tfsdk.Plan, tfsdk.State, tfsdk.Config) {
	t.Helper()
	s := drSchema(t)
	raw := tftypes.NewValue(tftypes.String, "junk")
	return tfsdk.Plan{Schema: s.Schema, Raw: raw}, tfsdk.State{Schema: s.Schema, Raw: raw}, tfsdk.Config{Schema: s.Schema, Raw: raw}
}

// drPlanModel is a create-time plan for one radio: every Computed attribute the
// test does not set is unknown, as Terraform plans a new resource.
func drPlanModel(band string) deviceRadioModel {
	return deviceRadioModel{
		ID:             types.StringUnknown(),
		MAC:            types.StringValue(drMAC),
		Frequency:      types.StringValue(band),
		Channel:        types.StringUnknown(),
		ChannelWidth:   types.Int64Unknown(),
		TxPowerMode:    types.StringUnknown(),
		TxPower:        types.Int64Unknown(),
		MinRssiEnabled: types.BoolUnknown(),
		MinRssi:        types.Int64Unknown(),
	}
}

// drConfigModel is a configuration with only the identity set.
func drConfigModel(band string) deviceRadioModel {
	return deviceRadioModel{
		ID:             types.StringNull(),
		MAC:            types.StringValue(drMAC),
		Frequency:      types.StringValue(band),
		Channel:        types.StringNull(),
		ChannelWidth:   types.Int64Null(),
		TxPowerMode:    types.StringNull(),
		TxPower:        types.Int64Null(),
		MinRssiEnabled: types.BoolNull(),
		MinRssi:        types.Int64Null(),
	}
}

func drWantErr(t *testing.T, d diag.Diagnostics, want string) {
	t.Helper()
	if !d.HasError() {
		t.Fatalf("expected an error containing %q, got none", want)
	}
	for _, e := range d.Errors() {
		if strings.Contains(e.Summary()+" "+e.Detail(), want) {
			return
		}
	}
	t.Fatalf("no error contains %q: %v", want, d)
}

func drCreate(t *testing.T, r *deviceRadioResource, m deviceRadioModel) (deviceRadioModel, diag.Diagnostics) {
	t.Helper()
	resp := resource.CreateResponse{State: drSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: drPlan(t, m)}, &resp)
	var got deviceRadioModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	}
	return got, resp.Diagnostics
}

// --- Metadata / Schema / Configure ---------------------------------------------

func TestDeviceRadioMetadataAndSchema(t *testing.T) {
	var resp resource.MetadataResponse
	NewDeviceRadioResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_device_radio" {
		t.Errorf("type name = %q, want unifi_device_radio", resp.TypeName)
	}
	attrs := drSchema(t).Schema.GetAttributes()
	for _, name := range []string{"id", "mac", "frequency_ghz", "channel", "channel_width_mhz", "tx_power_mode", "tx_power_dbm", "min_rssi_enabled", "min_rssi_dbm"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("schema missing %q", name)
		}
	}
}

func TestDeviceRadioConfigure(t *testing.T) {
	r := NewDeviceRadioResource().(*deviceRadioResource)
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() || r.data != nil {
		t.Fatalf("nil provider data must be a no-op: %v", resp.Diagnostics)
	}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "bogus"}, &resp)
	drWantErr(t, resp.Diagnostics, "Unexpected provider data")
}

// --- Create ------------------------------------------------------------------

func TestDeviceRadioCreateChangesOnlyItsRadio(t *testing.T) {
	ap := &drAP{table: drTable()}
	m := drPlanModel("5")
	m.Channel = types.StringValue("100")
	m.ChannelWidth = types.Int64Value(80)

	got, d := drCreate(t, drRes(t, ap.client()), m)
	if d.HasError() {
		t.Fatalf("create: %v", d)
	}
	if len(ap.putPaths) != 1 || ap.putPaths[0] != "s/default/rest/device/"+drDeviceID {
		t.Errorf("PUT paths = %v, want one PUT to the device's rest path", ap.putPaths)
	}
	na := ap.entry("na")
	if na["channel"] != "100" || na["ht"] != "80" {
		t.Errorf("5 GHz entry = %v, want channel and ht written in their existing string form", na)
	}
	if na["future_field"] != "kept" || na["tx_power_mode"] != "high" || na["name"] != "wifi1" {
		t.Errorf("unmanaged keys of the 5 GHz entry changed: %v", na)
	}
	if ng := ap.entry("ng"); ng["channel"] != json.Number("6") || ng["vwire_enabled"] != true {
		t.Errorf("2.4 GHz entry changed: %v", ng)
	}
	want := deviceRadioModel{
		ID:             types.StringValue(drMAC + "/5"),
		MAC:            types.StringValue(drMAC),
		Frequency:      types.StringValue("5"),
		Channel:        types.StringValue("100"),
		ChannelWidth:   types.Int64Value(80),
		TxPowerMode:    types.StringValue("high"),
		TxPower:        types.Int64Null(), // the controller's "auto"
		MinRssiEnabled: types.BoolValue(false),
		MinRssi:        types.Int64Null(),
	}
	if got != want {
		t.Errorf("state = %+v, want %+v", got, want)
	}
}

// withReadback shortens the read-back wait for one test.
func withReadback(t *testing.T, timeout, interval time.Duration) {
	t.Helper()
	oldTimeout, oldInterval := readbackTimeout, readbackInterval
	readbackTimeout, readbackInterval = timeout, interval
	t.Cleanup(func() { readbackTimeout, readbackInterval = oldTimeout, oldInterval })
}

// TestDeviceRadioCreateWaitsForTheWriteToShow covers the controller answering
// with the old radio table for a moment after the PUT (seen live as Terraform's
// "inconsistent result after apply"): write re-reads until the change shows.
func TestDeviceRadioCreateWaitsForTheWriteToShow(t *testing.T) {
	withReadback(t, time.Second, time.Millisecond)
	ap := &drAP{table: drTable(), staleGets: 2}
	m := drPlanModel("5")
	m.TxPowerMode = types.StringValue("medium")

	got, d := drCreate(t, drRes(t, ap.client()), m)
	if d.HasError() {
		t.Fatalf("create: %v", d)
	}
	if got.TxPowerMode.ValueString() != "medium" {
		t.Errorf("tx_power_mode = %v, want medium once the controller shows the write", got.TxPowerMode)
	}
	if ap.gets != 4 {
		t.Errorf("GETs = %d, want 4: one before the PUT, two stale, one current", ap.gets)
	}
}

// TestDeviceRadioCreateStopsWaitingAtTheDeadline returns what the controller
// reports once readbackTimeout passes, so a write it refused still surfaces.
func TestDeviceRadioCreateStopsWaitingAtTheDeadline(t *testing.T) {
	withReadback(t, 20*time.Millisecond, time.Millisecond)
	ap := &drAP{table: drTable(), staleGets: 1 << 20}
	m := drPlanModel("5")
	m.TxPowerMode = types.StringValue("medium")

	got, d := drCreate(t, drRes(t, ap.client()), m)
	if d.HasError() {
		t.Fatalf("create: %v", d)
	}
	if got.TxPowerMode.ValueString() != "high" {
		t.Errorf("tx_power_mode = %v, want the controller's high after the deadline", got.TxPowerMode)
	}
	if ap.gets < 3 {
		t.Errorf("GETs = %d, want several read-backs before giving up", ap.gets)
	}
}

// TestDeviceRadioCreateWaitHonoursCancel stops re-reading when the context ends.
func TestDeviceRadioCreateWaitHonoursCancel(t *testing.T) {
	withReadback(t, time.Minute, time.Minute)
	ap := &drAP{table: drTable(), staleGets: 1 << 20}
	m := drPlanModel("5")
	m.TxPowerMode = types.StringValue("medium")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := drRes(t, ap.client())
	resp := resource.CreateResponse{State: drSchema(t)}
	r.Create(ctx, resource.CreateRequest{Plan: drPlan(t, m)}, &resp)
	drWantErr(t, resp.Diagnostics, "Stopped waiting for the radio update")
}

func TestDeviceRadioCreateKeepsEachKeysJSONType(t *testing.T) {
	ap := &drAP{table: drTable()}
	r := drRes(t, ap.client())

	m := drPlanModel("2.4")
	m.Channel = types.StringValue("11")
	m.TxPowerMode = types.StringValue("custom")
	m.TxPower = types.Int64Value(12)
	m.MinRssiEnabled = types.BoolValue(true)
	m.MinRssi = types.Int64Value(-75)
	got, d := drCreate(t, r, m)
	if d.HasError() {
		t.Fatalf("create 2.4: %v", d)
	}
	ng := ap.entry("ng")
	if ng["channel"] != json.Number("11") || ng["tx_power"] != json.Number("12") || ng["min_rssi"] != json.Number("-75") {
		t.Errorf("2.4 GHz entry = %v, want numbers kept as numbers and new keys written as numbers", ng)
	}
	if ng["tx_power_mode"] != "custom" || ng["min_rssi_enabled"] != true {
		t.Errorf("2.4 GHz entry = %v, want custom power and min RSSI on", ng)
	}
	if got.TxPower.ValueInt64() != 12 || got.MinRssi.ValueInt64() != -75 || got.ChannelWidth.ValueInt64() != 20 || got.Channel.ValueString() != "11" {
		t.Errorf("state = %+v", got)
	}

	m = drPlanModel("6")
	m.Channel = types.StringValue("37")
	m.ChannelWidth = types.Int64Value(320)
	if _, d := drCreate(t, r, m); d.HasError() {
		t.Fatalf("create 6: %v", d)
	}
	if e := ap.entry("6e"); e["channel"] != "37" || e["ht"] != json.Number("320") {
		t.Errorf("6 GHz entry = %v, want channel as a string (was \"auto\") and ht as a number", e)
	}

	m = drPlanModel("6")
	m.Channel = types.StringValue("auto")
	if _, d := drCreate(t, r, m); d.HasError() {
		t.Fatalf("create 6 auto: %v", d)
	}
	if e := ap.entry("6e"); e["channel"] != "auto" || e["ht"] != json.Number("320") {
		t.Errorf("6 GHz entry = %v, want channel auto and the earlier width kept", e)
	}
}

func TestDeviceRadioCreateErrors(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		_, d := drCreate(t, drRes(t, &unifi.ClientMock{}, testutil.ReadOnly()), drPlanModel("5"))
		drWantErr(t, d, "read-only")
	})
	t.Run("bad plan", func(t *testing.T) {
		plan, _, _ := drJunk(t)
		resp := resource.CreateResponse{State: drSchema(t)}
		drRes(t, &unifi.ClientMock{}).Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected plan.Get to fail on a mistyped raw value")
		}
	})
	t.Run("device not found", func(t *testing.T) {
		_, d := drCreate(t, drRes(t, (&drAP{missing: true}).client()), drPlanModel("5"))
		drWantErr(t, d, "Failed to read the access point")
	})
	t.Run("no such radio", func(t *testing.T) {
		ap := &drAP{table: drTable()[:2]}
		_, d := drCreate(t, drRes(t, ap.client()), drPlanModel("6"))
		drWantErr(t, d, "has no 6 GHz radio")
	})
	t.Run("put fails", func(t *testing.T) {
		ap := &drAP{table: drTable(), putErr: errors.New("boom")}
		_, d := drCreate(t, drRes(t, ap.client()), drWiden("5"))
		drWantErr(t, d, "Failed to update the radio")
	})
	t.Run("read back fails", func(t *testing.T) {
		ap := &drAP{table: drTable(), getErr: errors.New("flaky"), getErrAt: 2}
		_, d := drCreate(t, drRes(t, ap.client()), drWiden("5"))
		drWantErr(t, d, "Failed to read the radio back")
	})
	t.Run("radio gone after update", func(t *testing.T) {
		ap := &drAP{table: drTable(), afterPut: func(t []map[string]any) []map[string]any { return t[:1] }}
		_, d := drCreate(t, drRes(t, ap.client()), drWiden("5"))
		drWantErr(t, d, "after the update")
	})
}

// drWiden plans a width the fixture radio does not have yet, so a write happens.
func drWiden(band string) deviceRadioModel {
	m := drPlanModel(band)
	m.ChannelWidth = types.Int64Value(80)
	return m
}

// A plan that changes nothing must not write: every PUT re-provisions the AP.
func TestDeviceRadioCreateSkipsWriteWhenNothingChanges(t *testing.T) {
	ap := &drAP{table: drTable()}
	r := drRes(t, ap.client())

	got, d := drCreate(t, r, drPlanModel("5"))
	if d.HasError() {
		t.Fatalf("adopt: %v", d)
	}
	if got.Channel.ValueString() != "149" || got.ChannelWidth.ValueInt64() != 40 || got.TxPowerMode.ValueString() != "high" {
		t.Errorf("adopted state = %+v, want the controller's current 5 GHz values", got)
	}

	m := drPlanModel("5")
	m.Channel = types.StringValue("149")
	m.ChannelWidth = types.Int64Value(40)
	m.TxPowerMode = types.StringValue("high")
	m.MinRssiEnabled = types.BoolValue(false)
	if _, d := drCreate(t, r, m); d.HasError() {
		t.Fatalf("matching plan: %v", d)
	}
	if len(ap.putPaths) != 0 {
		t.Errorf("PUTs = %v, want none for plans that match the controller", ap.putPaths)
	}
}

// Three radios of one AP applied in parallel must not undo each other: each
// write replaces the whole radio table, so only the per-AP lock keeps them apart.
func TestDeviceRadioParallelRadiosKeepEachOthersChanges(t *testing.T) {
	ap := &drAP{table: drTable(), getDelay: 20 * time.Millisecond}
	r := drRes(t, ap.client())
	widths := map[string]int64{"2.4": 40, "5": 80, "6": 320}
	plans := map[string]tfsdk.Plan{}
	for band, w := range widths {
		m := drPlanModel(band)
		m.ChannelWidth = types.Int64Value(w)
		plans[band] = drPlan(t, m)
	}
	var wg sync.WaitGroup
	for band, plan := range plans {
		wg.Add(1)
		go func(band string, plan tfsdk.Plan, state tfsdk.State) {
			defer wg.Done()
			resp := resource.CreateResponse{State: state}
			r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
			if resp.Diagnostics.HasError() {
				t.Errorf("create %s: %v", band, resp.Diagnostics)
			}
		}(band, plan, drSchema(t))
	}
	wg.Wait()
	if ap.entry("ng")["ht"] != json.Number("40") || ap.entry("na")["ht"] != "80" || ap.entry("6e")["ht"] != json.Number("320") {
		t.Errorf("a parallel write was lost: ng=%v na=%v 6e=%v", ap.entry("ng")["ht"], ap.entry("na")["ht"], ap.entry("6e")["ht"])
	}
}

// --- Read --------------------------------------------------------------------

func drReadResp(t *testing.T, r *deviceRadioResource, m deviceRadioModel) resource.ReadResponse {
	t.Helper()
	st := drState(t, m)
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	return resp
}

func drStored(band string) deviceRadioModel {
	return deviceRadioModel{
		ID:             types.StringValue(drMAC + "/" + band),
		MAC:            types.StringValue(drMAC),
		Frequency:      types.StringValue(band),
		Channel:        types.StringValue("36"),
		ChannelWidth:   types.Int64Value(80),
		TxPowerMode:    types.StringValue("auto"),
		TxPower:        types.Int64Null(),
		MinRssiEnabled: types.BoolValue(false),
		MinRssi:        types.Int64Null(),
	}
}

func TestDeviceRadioReadRefreshesFromController(t *testing.T) {
	ap := &drAP{table: drTable()}
	resp := drReadResp(t, drRes(t, ap.client()), drStored("6"))
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got deviceRadioModel
	resp.State.Get(context.Background(), &got)
	if got.Channel.ValueString() != "auto" || got.ChannelWidth.ValueInt64() != 160 || !got.MinRssiEnabled.ValueBool() || got.MinRssi.ValueInt64() != -80 {
		t.Errorf("state = %+v, want the controller's 6 GHz values", got)
	}
}

func TestDeviceRadioReadRemovesWhatIsGone(t *testing.T) {
	cases := map[string]*drAP{
		"device gone":    {missing: true},
		"http 404":       {getErr: &unifi.ServerError{StatusCode: 404}},
		"radio gone":     {table: drTable()[:1]},
		"other AP radio": {table: []map[string]any{{"radio": "ad"}}},
	}
	for name, ap := range cases {
		t.Run(name, func(t *testing.T) {
			resp := drReadResp(t, drRes(t, ap.client()), drStored("5"))
			if resp.Diagnostics.HasError() {
				t.Fatalf("read: %v", resp.Diagnostics)
			}
			if !resp.State.Raw.IsNull() {
				t.Error("resource should be removed from state")
			}
		})
	}
}

func TestDeviceRadioReadErrors(t *testing.T) {
	t.Run("api error", func(t *testing.T) {
		resp := drReadResp(t, drRes(t, (&drAP{getErr: errors.New("boom")}).client()), drStored("5"))
		drWantErr(t, resp.Diagnostics, "Failed to read the access point")
	})
	t.Run("undecodable record", func(t *testing.T) {
		c := &unifi.ClientMock{GetFunc: func(_ context.Context, _ string, _ any, respBody any) error {
			return json.Unmarshal([]byte(`{"data":["junk"]}`), respBody)
		}}
		resp := drReadResp(t, drRes(t, c), drStored("5"))
		drWantErr(t, resp.Diagnostics, "decoding the device record")
	})
	t.Run("bad state", func(t *testing.T) {
		_, st, _ := drJunk(t)
		resp := resource.ReadResponse{State: st}
		drRes(t, &unifi.ClientMock{}).Read(context.Background(), resource.ReadRequest{State: st}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected state.Get to fail on a mistyped raw value")
		}
	})
}

// --- Update ------------------------------------------------------------------

func drUpdate(t *testing.T, r *deviceRadioResource, plan tfsdk.Plan) resource.UpdateResponse {
	t.Helper()
	st := drState(t, drStored("5"))
	resp := resource.UpdateResponse{State: st}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: st}, &resp)
	return resp
}

func TestDeviceRadioUpdate(t *testing.T) {
	ap := &drAP{table: drTable()}
	m := drStored("5")
	m.Channel = types.StringValue("100")
	resp := drUpdate(t, drRes(t, ap.client()), drPlan(t, m))
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	if e := ap.entry("na"); e["channel"] != "100" || e["ht"] != "80" || e["tx_power_mode"] != "auto" {
		t.Errorf("5 GHz entry = %v, want every known planned value written", e)
	}
}

func TestDeviceRadioUpdateErrors(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		resp := drUpdate(t, drRes(t, &unifi.ClientMock{}, testutil.ReadOnly()), drPlan(t, drStored("5")))
		drWantErr(t, resp.Diagnostics, "read-only")
	})
	t.Run("bad plan", func(t *testing.T) {
		plan, _, _ := drJunk(t)
		resp := drUpdate(t, drRes(t, &unifi.ClientMock{}), plan)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected plan.Get to fail on a mistyped raw value")
		}
	})
	t.Run("put fails", func(t *testing.T) {
		ap := &drAP{table: drTable(), putErr: errors.New("boom")}
		resp := drUpdate(t, drRes(t, ap.client()), drPlan(t, drStored("5")))
		drWantErr(t, resp.Diagnostics, "Failed to update the radio")
	})
}

// --- Delete / Import / ValidateConfig ------------------------------------------

func TestDeviceRadioDelete(t *testing.T) {
	for name, opts := range map[string][]testutil.Opt{
		"read-only":          {testutil.ReadOnly()},
		"destroy protection": {testutil.DestroyProtection()},
	} {
		t.Run(name, func(t *testing.T) {
			var resp resource.DeleteResponse
			drRes(t, &unifi.ClientMock{}, opts...).Delete(context.Background(), resource.DeleteRequest{}, &resp)
			drWantErr(t, resp.Diagnostics, "Delete blocked")
		})
	}
	// An unstubbed mock panics on any call, so this also proves delete never touches the AP.
	var resp resource.DeleteResponse
	drRes(t, &unifi.ClientMock{}).Delete(context.Background(), resource.DeleteRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
}

func TestDeviceRadioImportState(t *testing.T) {
	r := drRes(t, &unifi.ClientMock{})
	resp := resource.ImportStateResponse{State: drSchema(t)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "00:00:5E:00:53:01/6"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import: %v", resp.Diagnostics)
	}
	var id, mac, band types.String
	resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
	resp.State.GetAttribute(context.Background(), path.Root("mac"), &mac)
	resp.State.GetAttribute(context.Background(), path.Root("frequency_ghz"), &band)
	if id.ValueString() != drMAC+"/6" || mac.ValueString() != "00:00:5E:00:53:01" || band.ValueString() != "6" {
		t.Errorf("imported id=%s mac=%s band=%s", id, mac, band)
	}
	for _, bad := range []string{"nope", drMAC + "/7", "zz:00:5e:00:53:01/5", "/5"} {
		resp := resource.ImportStateResponse{State: drSchema(t)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: bad}, &resp)
		drWantErr(t, resp.Diagnostics, "Invalid import ID")
	}
}

func drValidate(t *testing.T, m deviceRadioModel) diag.Diagnostics {
	t.Helper()
	s := drSchema(t)
	if d := s.Set(context.Background(), m); d.HasError() {
		t.Fatalf("config set: %v", d)
	}
	var resp resource.ValidateConfigResponse
	NewDeviceRadioResource().(*deviceRadioResource).ValidateConfig(context.Background(),
		resource.ValidateConfigRequest{Config: tfsdk.Config(s)}, &resp)
	return resp.Diagnostics
}

func TestDeviceRadioValidateConfig(t *testing.T) {
	m := drConfigModel("5")
	m.TxPower = types.Int64Value(10)
	drWantErr(t, drValidate(t, m), "tx_power_mode = \"custom\"")
	m.TxPowerMode = types.StringValue("high")
	drWantErr(t, drValidate(t, m), "tx_power_mode = \"custom\"")
	m.TxPowerMode = types.StringValue("custom")
	if d := drValidate(t, m); d.HasError() {
		t.Errorf("custom power with dBm should validate: %v", d)
	}
	m.TxPowerMode = types.StringUnknown()
	if d := drValidate(t, m); d.HasError() {
		t.Errorf("an unknown mode must not fail validation: %v", d)
	}

	m = drConfigModel("5")
	m.MinRssi = types.Int64Value(-75)
	drWantErr(t, drValidate(t, m), "min_rssi_enabled = true")
	m.MinRssiEnabled = types.BoolValue(false)
	drWantErr(t, drValidate(t, m), "min_rssi_enabled = true")
	m.MinRssiEnabled = types.BoolValue(true)
	if d := drValidate(t, m); d.HasError() {
		t.Errorf("enabled min RSSI with a value should validate: %v", d)
	}
	m.MinRssiEnabled = types.BoolUnknown()
	if d := drValidate(t, m); d.HasError() {
		t.Errorf("an unknown toggle must not fail validation: %v", d)
	}

	_, _, cfg := drJunk(t)
	var resp resource.ValidateConfigResponse
	NewDeviceRadioResource().(*deviceRadioResource).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected config.Get to fail on a mistyped raw value")
	}
}

// --- Value helpers -------------------------------------------------------------

func TestDeviceRadioValueHelpers(t *testing.T) {
	for _, c := range []struct {
		cur           any
		stringDefault bool
		want          any
	}{
		{json.Number("1"), true, json.Number("5")},
		{"x", false, "5"},
		{nil, true, "5"},
		{nil, false, json.Number("5")},
		{true, false, json.Number("5")},
	} {
		if got := sameKind(c.cur, "5", c.stringDefault); got != c.want {
			t.Errorf("sameKind(%#v, %v) = %#v, want %#v", c.cur, c.stringDefault, got, c.want)
		}
	}
	for in, want := range map[any]types.String{
		"auto":            types.StringValue("auto"),
		"":                types.StringNull(),
		json.Number("36"): types.StringValue("36"),
		true:              types.StringNull(),
	} {
		if got := stringOrNull(in); got != want {
			t.Errorf("stringOrNull(%#v) = %v, want %v", in, got, want)
		}
	}
	if got := stringOrNull(nil); !got.IsNull() {
		t.Errorf("stringOrNull(nil) = %v, want null", got)
	}
	for in, want := range map[any]types.Int64{
		json.Number("80"):  types.Int64Value(80),
		"40":               types.Int64Value(40),
		"auto":             types.Int64Null(),
		json.Number("1.5"): types.Int64Null(),
		false:              types.Int64Null(),
	} {
		if got := int64OrNull(in); got != want {
			t.Errorf("int64OrNull(%#v) = %v, want %v", in, got, want)
		}
	}
	if radioEntry(nil, "na") != nil {
		t.Error("radioEntry on an empty table should be nil")
	}
}
