package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipowm/go-unifi/v2/unifi"

	"github.com/PjSalty/terraform-provider-unifi/internal/providerdata"
)

var (
	_ resource.Resource                   = &deviceRadioResource{}
	_ resource.ResourceWithConfigure      = &deviceRadioResource{}
	_ resource.ResourceWithImportState    = &deviceRadioResource{}
	_ resource.ResourceWithValidateConfig = &deviceRadioResource{}
)

// radioCodes maps the provider's band strings to the radio codes in the
// controller's radio_table.
var radioCodes = map[string]string{"2.4": "ng", "5": "na", "6": "6e"}

var (
	macPattern     = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)
	channelPattern = regexp.MustCompile(`^(auto|[1-9]\d{0,2})$`)
)

// deviceLocks serializes the read-modify-write of one AP's radio_table. Every
// radio of an AP lives in that one table, so two radios applied in parallel
// would otherwise each write back the other's old values.
var deviceLocks sync.Map

// How long write keeps re-reading a radio until the controller reports the
// values it just accepted, and how often. Variables so tests can shorten them.
var (
	readbackTimeout  = 15 * time.Second
	readbackInterval = time.Second
)

func lockDevice(key string) func() {
	v, _ := deviceLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// NewDeviceRadioResource returns the unifi_device_radio resource.
func NewDeviceRadioResource() resource.Resource {
	return &deviceRadioResource{}
}

type deviceRadioResource struct {
	data *providerdata.Data
}

type deviceRadioModel struct {
	ID             types.String `tfsdk:"id"`
	MAC            types.String `tfsdk:"mac"`
	Frequency      types.String `tfsdk:"frequency_ghz"`
	Channel        types.String `tfsdk:"channel"`
	ChannelWidth   types.Int64  `tfsdk:"channel_width_mhz"`
	TxPowerMode    types.String `tfsdk:"tx_power_mode"`
	TxPower        types.Int64  `tfsdk:"tx_power_dbm"`
	MinRssiEnabled types.Bool   `tfsdk:"min_rssi_enabled"`
	MinRssi        types.Int64  `tfsdk:"min_rssi_dbm"`
}

// legacyDevice is the slice of a stat/device record this resource needs. The
// radio entries stay generic maps, so writing the table back keeps every field
// the controller returned, including ones newer than any client library models.
type legacyDevice struct {
	ID         string           `json:"_id"`
	RadioTable []map[string]any `json:"radio_table"`
}

func (r *deviceRadioResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_radio"
}

func (r *deviceRadioResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Channel, channel width, transmit power and minimum RSSI for one radio of an adopted access " +
			"point. The official Integration API cannot change radio settings, so this resource uses the " +
			"controller's internal device endpoint, the one the UniFi console itself calls, with the provider's " +
			"API key. The provider's `site` must be the site's internal name (\"default\" for the default site). " +
			"Attributes left out of the configuration are read from the controller and left as they are. " +
			"Destroying the resource only stops managing the radio: its settings stay as they were.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "`<mac>/<frequency_ghz>`, with the MAC in lower case.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mac": schema.StringAttribute{
				Required:      true,
				Description:   "MAC address of the access point, as the controller lists it (aa:bb:cc:dd:ee:ff).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(macPattern, "must be a colon-separated MAC address"),
				},
			},
			"frequency_ghz": schema.StringAttribute{
				Required:      true,
				Description:   "Which radio: \"2.4\", \"5\" or \"6\".",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf("2.4", "5", "6")},
			},
			"channel": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Primary channel number, or \"auto\" to let the controller choose.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(channelPattern, "must be \"auto\" or a channel number"),
				},
			},
			"channel_width_mhz": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Channel width in MHz: 20, 40, 80, 160, 240 or 320, as far as the radio and band allow.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.OneOf(20, 40, 80, 160, 240, 320)},
			},
			"tx_power_mode": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Transmit power: auto, low, medium, high, custom (uses tx_power_dbm) or disabled (radio off).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{
					stringvalidator.OneOf("auto", "low", "medium", "high", "custom", "disabled"),
				},
			},
			"tx_power_dbm": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Transmit power in dBm. Only with tx_power_mode = \"custom\".",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(1, 36)},
			},
			"min_rssi_enabled": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Disconnect clients whose signal falls below min_rssi_dbm, so they roam to a closer AP.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"min_rssi_dbm": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Minimum client signal in dBm (-90 to -67). Setting it needs min_rssi_enabled = true.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(-90, -67)},
			},
		},
	}
}

func (r *deviceRadioResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m deviceRadioModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.TxPower.IsNull() && !m.TxPowerMode.IsUnknown() && m.TxPowerMode.ValueString() != "custom" {
		resp.Diagnostics.AddAttributeError(path.Root("tx_power_dbm"), "tx_power_dbm needs tx_power_mode = \"custom\"",
			"The controller only applies a fixed power in custom mode. Set tx_power_mode = \"custom\" or drop tx_power_dbm.")
	}
	if !m.MinRssi.IsNull() && !m.MinRssiEnabled.IsUnknown() && !m.MinRssiEnabled.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("min_rssi_dbm"), "min_rssi_dbm needs min_rssi_enabled = true",
			"Set min_rssi_enabled = true alongside min_rssi_dbm, or drop min_rssi_dbm.")
	}
}

func (r *deviceRadioResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	d, ok := req.ProviderData.(*providerdata.Data)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *providerdata.Data, got %T. This is a provider bug.", req.ProviderData))
		return
	}
	r.data = d
}

func (r *deviceRadioResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to create resources.")
		return
	}
	var plan deviceRadioModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state, diags := r.write(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *deviceRadioResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deviceRadioModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dev, err := r.fetch(ctx, strings.ToLower(state.MAC.ValueString()))
	if errors.Is(err, unifi.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Failed to read the access point", err.Error())
		return
	}
	entry := radioEntry(dev.RadioTable, radioCodes[state.Frequency.ValueString()])
	if entry == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenRadio(state, entry))...)
}

func (r *deviceRadioResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.data.ReadOnly {
		resp.Diagnostics.AddError("Provider is read-only", "Unset read_only (or UNIFI_READ_ONLY) to update resources.")
		return
	}
	var plan deviceRadioModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state, diags := r.write(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Delete only drops the radio from state: the controller has no "unset" for a
// radio, and resetting it to defaults would surprise more than leaving it.
func (r *deviceRadioResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.data.ReadOnly || r.data.DestroyProtection {
		resp.Diagnostics.AddError("Delete blocked",
			"The provider is in read-only mode or destroy_protection is set; unset it to delete resources.")
	}
}

// ImportState takes "<mac>/<frequency_ghz>", e.g. "aa:bb:cc:dd:ee:ff/5".
func (r *deviceRadioResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	mac, band, ok := strings.Cut(req.ID, "/")
	if _, known := radioCodes[band]; !ok || !known || !macPattern.MatchString(mac) {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected \"<mac>/<frequency_ghz>\" with frequency_ghz 2.4, 5 or 6, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), radioID(mac, band))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), mac)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("frequency_ghz"), band)...)
}

// write applies the planned values to the radio and returns the state the
// controller reports afterwards. The per-AP lock spans the whole cycle.
func (r *deviceRadioResource) write(ctx context.Context, m deviceRadioModel) (deviceRadioModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	mac := strings.ToLower(m.MAC.ValueString())
	band := m.Frequency.ValueString()
	unlock := lockDevice(r.data.Site + "/" + mac)
	defer unlock()

	dev, err := r.fetch(ctx, mac)
	if err != nil {
		diags.AddError("Failed to read the access point", fmt.Sprintf("%s: %v", mac, err))
		return m, diags
	}
	entry := radioEntry(dev.RadioTable, radioCodes[band])
	if entry == nil {
		diags.AddError("Radio not found", fmt.Sprintf("Access point %s has no %s GHz radio.", mac, band))
		return m, diags
	}
	// Skip the write when the radio already matches: any PUT makes the
	// controller re-provision the AP, which briefly drops its clients.
	if setRadioFields(entry, m) {
		var out struct {
			Data []json.RawMessage `json:"data"`
		}
		body := map[string]any{"radio_table": dev.RadioTable}
		if err := r.data.Client.Put(ctx, fmt.Sprintf("s/%s/rest/device/%s", r.data.Site, dev.ID), body, &out); err != nil {
			diags.AddError("Failed to update the radio", fmt.Sprintf("%s %s GHz: %v", mac, band, err))
			return m, diags
		}
		// The controller can answer stat/device with the old radio table for a
		// few seconds after the PUT, which Terraform then reports as an
		// inconsistent result. Re-read until the radio shows the write, up to
		// readbackTimeout; after that the controller's values are returned as
		// they are, so a write it really refused still surfaces.
		deadline := time.Now().Add(readbackTimeout)
		for {
			dev, err = r.fetch(ctx, mac)
			if err != nil {
				diags.AddError("Failed to read the radio back", fmt.Sprintf("%s: %v", mac, err))
				return m, diags
			}
			entry = radioEntry(dev.RadioTable, radioCodes[band])
			if entry == nil {
				diags.AddError("Radio not found", fmt.Sprintf("Access point %s has no %s GHz radio after the update.", mac, band))
				return m, diags
			}
			if !setRadioFields(maps.Clone(entry), m) || !time.Now().Before(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				diags.AddError("Stopped waiting for the radio update", fmt.Sprintf("%s %s GHz: %v", mac, band, ctx.Err()))
				return m, diags
			case <-time.After(readbackInterval):
			}
		}
	}
	m.ID = types.StringValue(radioID(mac, band))
	return flattenRadio(m, entry), diags
}

// fetch reads one AP's record from stat/device. Numbers decode as json.Number so
// writing the table back reproduces them exactly.
func (r *deviceRadioResource) fetch(ctx context.Context, mac string) (*legacyDevice, error) {
	// shortcut: the configured site string doubles as the internal site name;
	// resolve it through the sites list if a display name is ever configured.
	var resp struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := r.data.Client.Get(ctx, fmt.Sprintf("s/%s/stat/device/%s", r.data.Site, mac), nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, unifi.ErrNotFound
	}
	dec := json.NewDecoder(bytes.NewReader(resp.Data[0]))
	dec.UseNumber()
	var d legacyDevice
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("decoding the device record: %w", err)
	}
	return &d, nil
}

func radioID(mac, band string) string {
	return strings.ToLower(mac) + "/" + band
}

func radioEntry(table []map[string]any, code string) map[string]any {
	for _, e := range table {
		if e["radio"] == code {
			return e
		}
	}
	return nil
}

// setRadioFields writes each known planned value into the radio entry and
// reports whether any value changed. A value keeps the JSON type the controller
// already uses for that key, because the controller stores some numbers as
// strings.
func setRadioFields(e map[string]any, m deviceRadioModel) bool {
	changed := false
	set := func(key string, v any) {
		if !reflect.DeepEqual(e[key], v) {
			e[key] = v
			changed = true
		}
	}
	if s := m.Channel; !s.IsNull() && !s.IsUnknown() {
		if s.ValueString() == "auto" {
			set("channel", "auto")
		} else {
			set("channel", sameKind(e["channel"], s.ValueString(), true))
		}
	}
	if v := m.ChannelWidth; !v.IsNull() && !v.IsUnknown() {
		set("ht", sameKind(e["ht"], strconv.FormatInt(v.ValueInt64(), 10), false))
	}
	if s := m.TxPowerMode; !s.IsNull() && !s.IsUnknown() {
		set("tx_power_mode", s.ValueString())
	}
	if v := m.TxPower; !v.IsNull() && !v.IsUnknown() {
		set("tx_power", sameKind(e["tx_power"], strconv.FormatInt(v.ValueInt64(), 10), false))
	}
	if b := m.MinRssiEnabled; !b.IsNull() && !b.IsUnknown() {
		set("min_rssi_enabled", b.ValueBool())
	}
	if v := m.MinRssi; !v.IsNull() && !v.IsUnknown() {
		set("min_rssi", sameKind(e["min_rssi"], strconv.FormatInt(v.ValueInt64(), 10), false))
	}
	return changed
}

// sameKind returns digits as a JSON number when the current value is a number,
// as a string when it is a string, and otherwise per stringDefault.
func sameKind(cur any, digits string, stringDefault bool) any {
	switch cur.(type) {
	case json.Number:
		return json.Number(digits)
	case string:
		return digits
	}
	if stringDefault {
		return digits
	}
	return json.Number(digits)
}

func flattenRadio(m deviceRadioModel, e map[string]any) deviceRadioModel {
	m.Channel = stringOrNull(e["channel"])
	m.ChannelWidth = int64OrNull(e["ht"])
	m.TxPowerMode = stringOrNull(e["tx_power_mode"])
	m.TxPower = int64OrNull(e["tx_power"])
	enabled, _ := e["min_rssi_enabled"].(bool)
	m.MinRssiEnabled = types.BoolValue(enabled)
	m.MinRssi = int64OrNull(e["min_rssi"])
	return m
}

func stringOrNull(v any) types.String {
	switch t := v.(type) {
	case string:
		if t != "" {
			return types.StringValue(t)
		}
	case json.Number:
		return types.StringValue(t.String())
	}
	return types.StringNull()
}

// int64OrNull reads a number the controller may send as a JSON number or as a
// numeric string; anything else (absent, "auto", "") is null.
func int64OrNull(v any) types.Int64 {
	var s string
	switch t := v.(type) {
	case json.Number:
		s = t.String()
	case string:
		s = t
	default:
		return types.Int64Null()
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return types.Int64Null()
	}
	return types.Int64Value(n)
}
