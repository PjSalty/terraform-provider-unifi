package resources_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/provider"
	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

const (
	protocolNetworkID    = "00000000-0000-4000-8000-000000000002"
	protocolZoneID       = "00000000-0000-4000-8000-000000000003"
	protocolCustomZoneID = "00000000-0000-4000-8000-000000000004"
)

// networkProtocolHarness exercises framework planning and CRUD against actual
// serialized HTTP requests, including unknowns that direct model tests omit.
type networkProtocolHarness struct {
	t            *testing.T
	server       tfprotov6.ProviderServer
	schema       *tfprotov6.Schema
	mu           sync.Mutex
	current      map[string]any
	writes       []map[string]any
	zoneReads    int
	networkReads int
	zoneFailure  bool
	readFailure  bool
	zones        []map[string]any
}

func newNetworkProtocolHarness(t *testing.T) *networkProtocolHarness {
	t.Helper()
	h := &networkProtocolHarness{t: t, zones: []map[string]any{
		{"id": protocolZoneID, "name": "Internal", "networkIds": []any{}, "metadata": map[string]any{"origin": "SYSTEM_DEFINED", "configurable": true}},
	}}
	api := httptest.NewTLSServer(http.HandlerFunc(h.serve))
	t.Cleanup(api.Close)
	h.server = providerserver.NewProtocol6(provider.New("test")())()
	schemas, err := h.server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	requireProtocolOK(t, err, schemas.Diagnostics)
	h.schema = schemas.ResourceSchemas["unifi_network"]
	cfg := protocolValue(schemas.Provider.ValueType(), map[string]any{
		"api_url": api.URL, "api_key": "test-only", "allow_insecure": true,
	})
	configured, err := h.server.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{
		Config: protocolDynamic(t, cfg), TerraformVersion: "1.15.9",
	})
	requireProtocolOK(t, err, configured.Diagnostics)
	return h
}

func (h *networkProtocolHarness) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/info"):
		_ = json.NewEncoder(w).Encode(map[string]any{"applicationVersion": "10.6.101"})
	case strings.HasSuffix(r.URL.Path, "/sites"):
		testutil.WriteEnvelope(w, []map[string]any{{"id": testutil.SiteID.String(), "internalReference": "default", "name": "Default"}})
	case strings.HasSuffix(r.URL.Path, "/firewall/zones"):
		h.zoneReads++
		if h.zoneFailure {
			http.Error(w, "zone lookup failed", http.StatusServiceUnavailable)
			return
		}
		testutil.WriteEnvelope(w, h.zones)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/networks/"+protocolNetworkID):
		h.networkReads++
		if h.readFailure {
			http.Error(w, "network read failed", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(h.current)
	case r.Method == http.MethodPost || r.Method == http.MethodPut:
		if !strings.Contains(r.URL.Path, "/networks") {
			http.Error(w, "unexpected write", http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.writes = append(h.writes, body)
		if body["management"] == "GATEWAY" {
			for _, key := range []string{"isolationEnabled", "cellularBackupEnabled", "internetAccessEnabled", "mdnsForwardingEnabled", "zoneId"} {
				if body[key] == nil {
					http.Error(w, key+" must not be null", http.StatusBadRequest)
					return
				}
			}
			ip := body["ipv4Configuration"].(map[string]any)
			if d, ok := ip["dhcpConfiguration"].(map[string]any); ok {
				if d["leaseTimeSeconds"] == nil || d["pingConflictDetectionEnabled"] == nil {
					http.Error(w, "required DHCP field missing", http.StatusBadRequest)
					return
				}
				if d["domainName"] == nil {
					d["domainName"] = ""
				}
			}
		}
		body["id"], body["default"] = protocolNetworkID, false
		body["metadata"] = map[string]any{"origin": "USER_DEFINED"}
		h.current = body
		_ = json.NewEncoder(w).Encode(body)
	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

func requireProtocolOK(t *testing.T, err error, diagnostics []*tfprotov6.Diagnostic) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("%s: %s", d.Summary, d.Detail)
		}
	}
}

// Fill missing object members with typed nulls without inventing schema defaults.
func protocolValue(typ tftypes.Type, raw any) tftypes.Value {
	if raw == nil {
		return tftypes.NewValue(typ, nil)
	}
	if obj, ok := typ.(tftypes.Object); ok {
		input := raw.(map[string]any)
		values := map[string]tftypes.Value{}
		for key, childType := range obj.AttributeTypes {
			values[key] = protocolValue(childType, input[key])
		}
		return tftypes.NewValue(typ, values)
	}
	return tftypes.NewValue(typ, raw)
}

func protocolDynamic(t *testing.T, value tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	d, err := tfprotov6.NewDynamicValue(value.Type(), value)
	if err != nil {
		t.Fatal(err)
	}
	return &d
}

func (h *networkProtocolHarness) decode(d *tfprotov6.DynamicValue) tftypes.Value {
	h.t.Helper()
	v, err := d.Unmarshal(h.schema.ValueType())
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// Match core's proposed-state merge: retain prior computed values for omitted
// configuration, recurse through present nested objects, leave absent parents null.
func protocolProposed(attrs []*tfprotov6.SchemaAttribute, config, prior tftypes.Value) tftypes.Value {
	config = config.Copy()
	var cfg, old map[string]tftypes.Value
	_ = config.As(&cfg)
	if !prior.IsNull() {
		_ = prior.As(&old)
	}
	for _, a := range attrs {
		c := cfg[a.Name]
		p, ok := old[a.Name]
		if !ok {
			p = tftypes.NewValue(c.Type(), nil)
		}
		if c.IsNull() && a.Computed {
			cfg[a.Name] = p
		} else if a.NestedType != nil && !c.IsNull() && c.IsKnown() {
			cfg[a.Name] = protocolProposed(a.NestedType.Attributes, c, p)
		}
	}
	return tftypes.NewValue(config.Type(), cfg)
}

func (h *networkProtocolHarness) plan(config, prior tftypes.Value) *tfprotov6.PlanResourceChangeResponse {
	h.t.Helper()
	validated, err := h.server.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: "unifi_network", Config: protocolDynamic(h.t, config),
	})
	requireProtocolOK(h.t, err, validated.Diagnostics)
	p, err := h.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "unifi_network", Config: protocolDynamic(h.t, config), PriorState: protocolDynamic(h.t, prior),
		ProposedNewState: protocolDynamic(h.t, protocolProposed(h.schema.Block.Attributes, config, prior)),
	})
	requireProtocolOK(h.t, err, p.Diagnostics)
	return p
}

func (h *networkProtocolHarness) apply(config, prior tftypes.Value, plan *tfprotov6.PlanResourceChangeResponse) tftypes.Value {
	h.t.Helper()
	applied, err := h.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "unifi_network", Config: protocolDynamic(h.t, config), PriorState: protocolDynamic(h.t, prior),
		PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate,
	})
	requireProtocolOK(h.t, err, applied.Diagnostics)
	state := h.decode(applied.NewState)
	// The protocol alone does not enforce core's known-value consistency rule.
	// Check known subtrees (including null parents/children) explicitly.
	err = tftypes.Walk(h.decode(plan.PlannedState), func(path *tftypes.AttributePath, value tftypes.Value) (bool, error) {
		if !value.IsKnown() {
			return false, nil
		}
		if !value.IsFullyKnown() {
			return true, nil
		}
		actual, _, walkErr := tftypes.WalkAttributePath(state, path)
		if walkErr != nil {
			return false, walkErr
		}
		if !value.Equal(actual.(tftypes.Value)) {
			return false, fmt.Errorf("inconsistent result at %s: planned %s, got %s", path, value, actual)
		}
		return false, nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	read, err := h.server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{
		TypeName: "unifi_network", CurrentState: applied.NewState, Private: applied.Private,
	})
	requireProtocolOK(h.t, err, read.Diagnostics)
	refreshed := h.decode(read.NewState)
	next := h.decode(h.plan(config, refreshed).PlannedState)
	if !next.Equal(refreshed) {
		h.t.Fatalf("subsequent plan differs: %s versus %s", next, refreshed)
	}
	return state
}

func protocolAt(t *testing.T, value tftypes.Value, names ...string) tftypes.Value {
	t.Helper()
	path := tftypes.NewAttributePath()
	for _, name := range names {
		path = path.WithAttributeName(name)
	}
	found, _, err := tftypes.WalkAttributePath(value, path)
	if err != nil {
		t.Fatal(err)
	}
	return found.(tftypes.Value)
}

func gatewayProtocolConfig(dhcp bool) map[string]any {
	gateway := map[string]any{"host_ip_address": "192.0.2.1", "prefix_length": float64(24)}
	if dhcp {
		gateway["dhcp"] = map[string]any{"range_start": "192.0.2.10", "range_stop": "192.0.2.20"}
	}
	return map[string]any{"name": "test-network", "vlan_id": float64(20), "management": "GATEWAY", "gateway": gateway}
}

func TestNetworkProtocolTransitions(t *testing.T) {
	for _, scenario := range []string{"minimal create", "add DHCP", "convert unmanaged", "convert with explicit booleans", "legacy state without refresh", "preserve custom zone", "recover custom zone"} {
		t.Run(scenario, func(t *testing.T) {
			h := newNetworkProtocolHarness(t)
			config := gatewayProtocolConfig(true)
			prior := tftypes.NewValue(h.schema.ValueType(), nil)
			if scenario == "add DHCP" || scenario == "preserve custom zone" || scenario == "recover custom zone" {
				initial := gatewayProtocolConfig(scenario != "add DHCP")
				if scenario != "add DHCP" {
					initial["gateway"].(map[string]any)["zone_id"] = protocolCustomZoneID
				}
				cfg := protocolValue(h.schema.ValueType(), initial)
				prior = h.apply(cfg, prior, h.plan(cfg, prior))
			}
			if strings.HasPrefix(scenario, "convert") {
				cfg := protocolValue(h.schema.ValueType(), map[string]any{"name": "test-network", "vlan_id": float64(20), "management": "UNMANAGED"})
				prior = h.apply(cfg, prior, h.plan(cfg, prior))
				if scenario == "convert with explicit booleans" {
					g := config["gateway"].(map[string]any)
					g["isolation_enabled"], g["cellular_backup_enabled"], g["internet_access_enabled"], g["mdns_forwarding_enabled"] = true, true, false, true
				}
			}
			if scenario == "legacy state without refresh" {
				legacy := gatewayProtocolConfig(true)
				legacy["id"], legacy["enabled"] = protocolNetworkID, true
				legacy["gateway"].(map[string]any)["auto_scale_enabled"] = false
				b, err := json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
				upgraded, err := h.server.UpgradeResourceState(context.Background(), &tfprotov6.UpgradeResourceStateRequest{
					TypeName: "unifi_network", Version: 0, RawState: &tfprotov6.RawState{JSON: b},
				})
				requireProtocolOK(t, err, upgraded.Diagnostics)
				prior = h.decode(upgraded.UpgradedState)
				h.current = map[string]any{"management": "GATEWAY", "id": protocolNetworkID, "name": "test-network", "enabled": true, "vlanId": 20, "zoneId": protocolCustomZoneID}
			}
			if scenario == "recover custom zone" {
				prior = prior.Copy()
				var obj, gw map[string]tftypes.Value
				_ = prior.As(&obj)
				_ = obj["gateway"].As(&gw)
				gw["zone_id"] = tftypes.NewValue(tftypes.String, nil)
				obj["gateway"] = tftypes.NewValue(obj["gateway"].Type(), gw)
				prior = tftypes.NewValue(prior.Type(), obj)
			}
			config["name"] = "renamed-network"
			cfg := protocolValue(h.schema.ValueType(), config)
			p := h.plan(cfg, prior)
			planned := h.decode(p.PlannedState)
			if scenario == "minimal create" || strings.HasPrefix(scenario, "convert") || scenario == "legacy state without refresh" {
				z := protocolAt(t, planned, "gateway", "zone_id")
				if z.IsKnown() {
					t.Fatalf("unresolved zone should remain unknown, got %s", z)
				}
			}
			for _, key := range []string{"isolation_enabled", "cellular_backup_enabled", "internet_access_enabled", "mdns_forwarding_enabled"} {
				if v := protocolAt(t, planned, "gateway", key); !v.IsKnown() || v.IsNull() {
					t.Fatalf("%s not resolved in plan: %s", key, v)
				}
			}
			if v := protocolAt(t, planned, "gateway", "dhcp", "lease_time_seconds"); !v.Equal(tftypes.NewValue(tftypes.Number, 86400)) {
				t.Fatalf("lease default: %s", v)
			}
			if v := protocolAt(t, planned, "gateway", "dhcp", "ping_conflict_detection_enabled"); !v.Equal(tftypes.NewValue(tftypes.Bool, true)) {
				t.Fatalf("conflict default: %s", v)
			}
			state := h.apply(cfg, prior, p)
			wantZone := protocolZoneID
			if strings.Contains(scenario, "custom zone") || scenario == "legacy state without refresh" {
				wantZone = protocolCustomZoneID
			}
			if !protocolAt(t, state, "gateway", "zone_id").Equal(tftypes.NewValue(tftypes.String, wantZone)) {
				t.Fatal("wrong zone")
			}
			if wantZone == protocolCustomZoneID && h.zoneReads != 0 {
				t.Fatal("existing custom zone triggered Internal lookup")
			}
		})
	}
}

func TestNetworkProtocolDefaultsAreVisible(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			h := newNetworkProtocolHarness(t)
			config := gatewayProtocolConfig(true)
			g := config["gateway"].(map[string]any)
			g["isolation_enabled"], g["cellular_backup_enabled"], g["internet_access_enabled"], g["mdns_forwarding_enabled"] = true, true, false, true
			d := g["dhcp"].(map[string]any)
			d["lease_time_seconds"], d["ping_conflict_detection_enabled"], d["domain_name"] = float64(3600), false, "example.test"
			cfg := protocolValue(h.schema.ValueType(), config)
			null := tftypes.NewValue(h.schema.ValueType(), nil)
			h.apply(cfg, null, h.plan(cfg, null))
			imported, err := h.server.ImportResourceState(context.Background(), &tfprotov6.ImportResourceStateRequest{
				TypeName: "unifi_network", ID: protocolNetworkID,
			})
			requireProtocolOK(t, err, imported.Diagnostics)
			read, err := h.server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{
				TypeName: "unifi_network", CurrentState: imported.ImportedResources[0].State,
			})
			requireProtocolOK(t, err, read.Diagnostics)
			prior := h.decode(read.NewState)
			if !explicit {
				for _, key := range []string{"isolation_enabled", "cellular_backup_enabled", "internet_access_enabled", "mdns_forwarding_enabled"} {
					delete(g, key)
				}
				delete(d, "lease_time_seconds")
				delete(d, "ping_conflict_detection_enabled")
			}
			config["name"] = "renamed"
			cfg = protocolValue(h.schema.ValueType(), config)
			p := h.plan(cfg, prior)
			want := 86400
			if explicit {
				want = 3600
			}
			if v := protocolAt(t, h.decode(p.PlannedState), "gateway", "dhcp", "lease_time_seconds"); !v.Equal(tftypes.NewValue(tftypes.Number, want)) {
				t.Fatalf("lease plan must show %d: %s", want, v)
			}
			h.apply(cfg, prior, p)
			ip := h.writes[len(h.writes)-1]["ipv4Configuration"].(map[string]any)
			if ip["dhcpConfiguration"].(map[string]any)["leaseTimeSeconds"] != float64(want) {
				t.Fatal("wrong lease on wire")
			}
		})
	}
}

func TestNetworkProtocolEmptyDomainRejected(t *testing.T) {
	h := newNetworkProtocolHarness(t)
	config := gatewayProtocolConfig(true)
	config["gateway"].(map[string]any)["dhcp"].(map[string]any)["domain_name"] = ""
	v, err := h.server.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: "unifi_network", Config: protocolDynamic(t, protocolValue(h.schema.ValueType(), config)),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range v.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return
		}
	}
	t.Fatal("empty domain accepted")
}

func TestNetworkProtocolClearDomain(t *testing.T) {
	h := newNetworkProtocolHarness(t)
	config := gatewayProtocolConfig(true)
	dhcp := config["gateway"].(map[string]any)["dhcp"].(map[string]any)
	dhcp["domain_name"] = "example.test"
	cfg := protocolValue(h.schema.ValueType(), config)
	null := tftypes.NewValue(h.schema.ValueType(), nil)
	prior := h.apply(cfg, null, h.plan(cfg, null))
	delete(dhcp, "domain_name")
	cfg = protocolValue(h.schema.ValueType(), config)
	state := h.apply(cfg, prior, h.plan(cfg, prior))
	if !protocolAt(t, state, "gateway", "dhcp", "domain_name").IsNull() {
		t.Fatal("omitted domain did not clear")
	}
}

func TestNetworkProtocolResolutionFailures(t *testing.T) {
	for _, scenario := range []string{"zone read", "missing Internal", "ambiguous Internal", "existing read", "missing existing zone"} {
		t.Run(scenario, func(t *testing.T) {
			h := newNetworkProtocolHarness(t)
			config := gatewayProtocolConfig(false)
			cfg := protocolValue(h.schema.ValueType(), config)
			prior := tftypes.NewValue(h.schema.ValueType(), nil)
			switch scenario {
			case "zone read":
				h.zoneFailure = true
			case "missing Internal":
				h.zones = nil
			case "ambiguous Internal":
				h.zones = append(h.zones, h.zones[0])
			default:
				config["id"] = protocolNetworkID
				prior = protocolValue(h.schema.ValueType(), config)
				h.current = map[string]any{"management": "GATEWAY", "id": protocolNetworkID}
				h.readFailure = scenario == "existing read"
			}
			p := h.plan(cfg, prior)
			result, err := h.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "unifi_network", Config: protocolDynamic(t, cfg), PriorState: protocolDynamic(t, prior), PlannedState: p.PlannedState,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(h.writes) != 0 {
				t.Fatal("wrote after failed zone resolution")
			}
			for _, d := range result.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError {
					return
				}
			}
			t.Fatal("expected diagnostic")
		})
	}
}

func TestNetworkProtocolUnmanagedWire(t *testing.T) {
	h := newNetworkProtocolHarness(t)
	cfg := protocolValue(h.schema.ValueType(), map[string]any{"name": "vlan-only", "vlan_id": float64(20), "management": "UNMANAGED"})
	null := tftypes.NewValue(h.schema.ValueType(), nil)
	h.apply(cfg, null, h.plan(cfg, null))
	got := h.writes[0]
	// Stub responses add read-only fields to the captured request.
	for _, key := range []string{"id", "default", "metadata"} {
		delete(got, key)
	}
	if !reflect.DeepEqual(got, map[string]any{"name": "vlan-only", "vlanId": float64(20), "management": "UNMANAGED", "enabled": true}) {
		t.Fatalf("UNMANAGED wire changed: %#v", got)
	}
	if h.zoneReads != 0 {
		t.Fatal("UNMANAGED performed zone discovery")
	}
}
