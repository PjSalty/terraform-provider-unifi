package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/filipowm/go-unifi/v2/unifi"
	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/PjSalty/terraform-provider-unifi/internal/testutil"
)

// --- harness -----------------------------------------------------------------

func fpSchema(t *testing.T) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	(&firewallPolicyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sr)
	if sr.Diagnostics.HasError() {
		t.Fatalf("schema: %v", sr.Diagnostics)
	}
	return tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
}

func fpResource(t *testing.T, fw *official.FirewallClientMock, opts ...testutil.Opt) *firewallPolicyResource {
	t.Helper()
	r, ok := NewFirewallPolicyResource().(*firewallPolicyResource)
	if !ok {
		t.Fatal("NewFirewallPolicyResource returned the wrong type")
	}
	oc := &official.ClientMock{FirewallFunc: func() official.FirewallClient { return fw }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure: %v", resp.Diagnostics)
	}
	return r
}

func fpPlan(t *testing.T, m firewallPolicyModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan(fpSchema(t))
	if d := p.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building plan: %v", d)
	}
	return p
}

func fpState(t *testing.T, m firewallPolicyModel) tfsdk.State {
	t.Helper()
	s := fpSchema(t)
	if d := s.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building state: %v", d)
	}
	return s
}

func fpWantErr(t *testing.T, diags diag.Diagnostics, want string) {
	t.Helper()
	if !diags.HasError() {
		t.Fatalf("expected an error diagnostic containing %q, got none", want)
	}
	for _, d := range diags.Errors() {
		if strings.Contains(d.Summary(), want) {
			return
		}
	}
	t.Fatalf("no error summary contains %q, got %v", want, diags)
}

var (
	fpSrcZone = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	fpDstZone = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	fpNetID   = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

func fpNullStr() types.List { return types.ListNull(types.StringType) }
func fpNullInt() types.List { return types.ListNull(types.Int64Type) }

func fpStrList(vals ...string) types.List {
	elems := make([]attr.Value, 0, len(vals))
	for _, v := range vals {
		elems = append(elems, types.StringValue(v))
	}
	return types.ListValueMust(types.StringType, elems)
}

func fpIntList(vals ...int64) types.List {
	elems := make([]attr.Value, 0, len(vals))
	for _, v := range vals {
		elems = append(elems, types.Int64Value(v))
	}
	return types.ListValueMust(types.Int64Type, elems)
}

// fpBaseModel is a valid ALLOW IPV4_AND_IPV6 network-to-network policy. Tests
// mutate one facet at a time.
func fpBaseModel() firewallPolicyModel {
	return firewallPolicyModel{
		ID:                    types.StringUnknown(),
		Name:                  types.StringValue("allow-iot-to-lan"),
		Enabled:               types.BoolValue(true),
		LoggingEnabled:        types.BoolValue(false),
		Description:           types.StringNull(),
		Index:                 types.Int64Unknown(),
		Origin:                types.StringUnknown(),
		ConnectionStateFilter: fpNullStr(),
		IpsecFilter:           types.StringNull(),
		Action:                &firewallPolicyActionModel{Type: types.StringValue("ALLOW"), AllowReturnTraffic: types.BoolNull()},
		IPProtocolScope:       &firewallPolicyScopeModel{IPVersion: types.StringValue("IPV4_AND_IPV6"), Protocol: types.StringNull()},
		Source: &firewallPolicySourceModel{
			ZoneID:      types.StringValue(fpSrcZone.String()),
			NetworkIDs:  fpStrList(fpNetID.String()),
			IPAddresses: fpNullStr(),
			Ports:       fpNullInt(),
		},
		Destination: &firewallPolicyDestinationModel{
			ZoneID:      types.StringValue(fpDstZone.String()),
			NetworkIDs:  fpNullStr(),
			IPAddresses: fpNullStr(),
			Ports:       fpNullInt(),
			Domains:     fpNullStr(),
		},
	}
}

// fpExpand runs expand and fails the test on unexpected diagnostics.
func fpExpand(t *testing.T, m firewallPolicyModel) official.FirewallPolicyCreateOrUpdate {
	t.Helper()
	body, diags := expandFirewallPolicy(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	return body
}

// fpJSON marshals a body to a generic map for discriminator/payload assertions.
func fpJSON(t *testing.T, body official.FirewallPolicyCreateOrUpdate) map[string]any {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func fpObj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("key %q is not an object in %v", key, m)
	}
	return v
}

// fpEcho maps a create/update body back into a read model, as the controller
// would echo it. The union sub-objects carry their own marshaled state, so
// flatten navigates them exactly as against a live response.
func fpEcho(id uuid.UUID, body official.FirewallPolicyCreateOrUpdate) *official.FirewallPolicy {
	return &official.FirewallPolicy{
		Id:                    id,
		Name:                  body.Name,
		Enabled:               body.Enabled,
		LoggingEnabled:        body.LoggingEnabled,
		Description:           body.Description,
		Index:                 7,
		Action:                body.Action,
		IpProtocolScope:       body.IpProtocolScope,
		Source:                body.Source,
		Destination:           body.Destination,
		ConnectionStateFilter: body.ConnectionStateFilter,
		IpsecFilter:           body.IpsecFilter,
		Metadata:              official.UserOrSystemDefinedOrDerivedEntityMetadata{Origin: "USER_DEFINED"},
	}
}

// --- Metadata / Schema / Configure --------------------------------------------

func TestFirewallPolicyMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	(&firewallPolicyResource{}).Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_firewall_policy" {
		t.Errorf("type name = %q, want unifi_firewall_policy", resp.TypeName)
	}
}

func TestFirewallPolicySchemaAttributes(t *testing.T) {
	s := fpSchema(t)
	for _, name := range []string{
		"id", "name", "enabled", "logging_enabled", "description", "index", "origin",
		"connection_state_filter", "ipsec_filter", "action", "ip_protocol_scope", "source", "destination",
	} {
		if _, ok := s.Schema.GetAttributes()[name]; !ok {
			t.Errorf("schema missing attribute %q", name)
		}
	}
}

func TestFirewallPolicyConfigure(t *testing.T) {
	r := &firewallPolicyResource{}

	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &resp)
	if resp.Diagnostics.HasError() || r.data != nil {
		t.Errorf("nil provider data should be a no-op, diags %v", resp.Diagnostics)
	}

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 42}, &resp)
	fpWantErr(t, resp.Diagnostics, "Unexpected provider data")
	if r.data != nil {
		t.Error("data should stay nil on wrong-type provider data")
	}

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(&official.ClientMock{})}, &resp)
	if resp.Diagnostics.HasError() || r.data == nil {
		t.Errorf("configure with provider data failed: %v", resp.Diagnostics)
	}
}

// --- expand: action -----------------------------------------------------------

func TestFirewallPolicyExpandActionAllow(t *testing.T) {
	m := fpBaseModel()
	m.Action = &firewallPolicyActionModel{Type: types.StringValue("ALLOW"), AllowReturnTraffic: types.BoolValue(true)}
	obj := fpJSON(t, fpExpand(t, m))
	act := fpObj(t, obj, "action")
	if act["type"] != "ALLOW" {
		t.Errorf("action.type = %v, want ALLOW", act["type"])
	}
	if act["allowReturnTraffic"] != true {
		t.Errorf("allowReturnTraffic = %v, want true", act["allowReturnTraffic"])
	}
}

func TestFirewallPolicyExpandActionAllowNoReturn(t *testing.T) {
	obj := fpJSON(t, fpExpand(t, fpBaseModel()))
	act := fpObj(t, obj, "action")
	if act["type"] != "ALLOW" {
		t.Errorf("action.type = %v, want ALLOW", act["type"])
	}
	if _, ok := act["allowReturnTraffic"]; ok {
		t.Errorf("allowReturnTraffic should be omitted when unset, got %v", act["allowReturnTraffic"])
	}
}

func TestFirewallPolicyExpandActionBlockReject(t *testing.T) {
	for _, typ := range []string{"BLOCK", "REJECT"} {
		t.Run(typ, func(t *testing.T) {
			m := fpBaseModel()
			m.Action = &firewallPolicyActionModel{Type: types.StringValue(typ), AllowReturnTraffic: types.BoolNull()}
			obj := fpJSON(t, fpExpand(t, m))
			if act := fpObj(t, obj, "action"); act["type"] != typ {
				t.Errorf("action.type = %v, want %s", act["type"], typ)
			}
		})
	}
}

func TestFirewallPolicyExpandActionInvalid(t *testing.T) {
	m := fpBaseModel()
	m.Action = &firewallPolicyActionModel{Type: types.StringValue("NOPE"), AllowReturnTraffic: types.BoolNull()}
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Invalid action type")
}

func TestFirewallPolicyExpandActionMissing(t *testing.T) {
	m := fpBaseModel()
	m.Action = nil
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Missing action")
}

// --- expand: ip_protocol_scope ------------------------------------------------

func TestFirewallPolicyExpandScope(t *testing.T) {
	cases := []struct {
		version, protocol string
	}{
		{"IPV4", "tcp"},
		{"IPV6", "icmpv6"},
		{"IPV4_AND_IPV6", "tcp_udp"},
		{"IPV4_AND_IPV6", ""},
	}
	for _, tc := range cases {
		name := tc.version + "/" + tc.protocol
		t.Run(name, func(t *testing.T) {
			m := fpBaseModel()
			proto := types.StringNull()
			if tc.protocol != "" {
				proto = types.StringValue(tc.protocol)
			}
			m.IPProtocolScope = &firewallPolicyScopeModel{IPVersion: types.StringValue(tc.version), Protocol: proto}
			obj := fpJSON(t, fpExpand(t, m))
			scope := fpObj(t, obj, "ipProtocolScope")
			if scope["ipVersion"] != tc.version {
				t.Errorf("ipVersion = %v, want %s", scope["ipVersion"], tc.version)
			}
			if tc.protocol == "" {
				if _, ok := scope["protocolFilter"]; ok {
					t.Errorf("protocolFilter should be omitted, got %v", scope["protocolFilter"])
				}
				return
			}
			pf := fpObj(t, scope, "protocolFilter")
			if pf["type"] != "NAMED_PROTOCOL" {
				t.Errorf("protocolFilter.type = %v, want NAMED_PROTOCOL", pf["type"])
			}
			named := fpObj(t, pf, "protocol")
			if named["name"] != tc.protocol {
				t.Errorf("protocol.name = %v, want %s", named["name"], tc.protocol)
			}
		})
	}
}

func TestFirewallPolicyExpandScopeInvalid(t *testing.T) {
	m := fpBaseModel()
	m.IPProtocolScope = &firewallPolicyScopeModel{IPVersion: types.StringValue("IPV5"), Protocol: types.StringNull()}
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Invalid ip_version")
}

// --- expand: source / destination filters -------------------------------------

func TestFirewallPolicyExpandSourceNetwork(t *testing.T) {
	obj := fpJSON(t, fpExpand(t, fpBaseModel()))
	src := fpObj(t, obj, "source")
	if src["zoneId"] != fpSrcZone.String() {
		t.Errorf("source.zoneId = %v, want %s", src["zoneId"], fpSrcZone)
	}
	tf := fpObj(t, src, "trafficFilter")
	if tf["type"] != "NETWORK" {
		t.Errorf("trafficFilter.type = %v, want NETWORK", tf["type"])
	}
	nf := fpObj(t, tf, "networkFilter")
	ids, ok := nf["networkIds"].([]any)
	if !ok || len(ids) != 1 || ids[0] != fpNetID.String() {
		t.Errorf("networkIds = %v, want [%s]", nf["networkIds"], fpNetID)
	}
}

func TestFirewallPolicyExpandSourceIPAddresses(t *testing.T) {
	m := fpBaseModel()
	m.Source = &firewallPolicySourceModel{
		ZoneID:      types.StringValue(fpSrcZone.String()),
		NetworkIDs:  fpNullStr(),
		IPAddresses: fpStrList("10.10.50.10", "10.10.50.11"),
		Ports:       fpNullInt(),
	}
	tf := fpObj(t, fpObj(t, fpJSON(t, fpExpand(t, m)), "source"), "trafficFilter")
	if tf["type"] != "IP_ADDRESS" {
		t.Errorf("trafficFilter.type = %v, want IP_ADDRESS", tf["type"])
	}
	ipf := fpObj(t, tf, "ipAddressFilter")
	if ipf["type"] != "IP_ADDRESSES" {
		t.Errorf("ipAddressFilter.type = %v, want IP_ADDRESSES", ipf["type"])
	}
	items, ok := ipf["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2 entries", ipf["items"])
	}
	first := items[0].(map[string]any)
	if first["type"] != "IP_ADDRESS" || first["value"] != "10.10.50.10" {
		t.Errorf("first item = %v", first)
	}
}

func TestFirewallPolicyExpandSourcePorts(t *testing.T) {
	m := fpBaseModel()
	m.Source = &firewallPolicySourceModel{
		ZoneID:      types.StringValue(fpSrcZone.String()),
		NetworkIDs:  fpNullStr(),
		IPAddresses: fpNullStr(),
		Ports:       fpIntList(53, 443),
	}
	tf := fpObj(t, fpObj(t, fpJSON(t, fpExpand(t, m)), "source"), "trafficFilter")
	if tf["type"] != "PORT" {
		t.Errorf("trafficFilter.type = %v, want PORT", tf["type"])
	}
	pf := fpObj(t, tf, "portFilter")
	if pf["type"] != "PORTS" {
		t.Errorf("portFilter.type = %v, want PORTS", pf["type"])
	}
	items, ok := pf["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2 entries", pf["items"])
	}
	first := items[0].(map[string]any)
	if first["type"] != "PORT_NUMBER" || first["value"].(float64) != 53 {
		t.Errorf("first port item = %v", first)
	}
}

func TestFirewallPolicyExpandSourceConflict(t *testing.T) {
	m := fpBaseModel()
	m.Source = &firewallPolicySourceModel{
		ZoneID:      types.StringValue(fpSrcZone.String()),
		NetworkIDs:  fpStrList(fpNetID.String()),
		IPAddresses: fpNullStr(),
		Ports:       fpIntList(443),
	}
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Conflicting source traffic filters")
}

func TestFirewallPolicyExpandSourceBadZone(t *testing.T) {
	m := fpBaseModel()
	m.Source.ZoneID = types.StringValue("not-a-uuid")
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Invalid source zone_id")
}

func TestFirewallPolicyExpandSourceBadNetworkID(t *testing.T) {
	m := fpBaseModel()
	m.Source.NetworkIDs = fpStrList("not-a-uuid")
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Invalid UUID in source network_ids")
}

func TestFirewallPolicyExpandSourceMissing(t *testing.T) {
	m := fpBaseModel()
	m.Source = nil
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Missing source")
}

func TestFirewallPolicyExpandDestinationVariants(t *testing.T) {
	base := fpBaseModel()
	dstZone := types.StringValue(fpDstZone.String())

	t.Run("network", func(t *testing.T) {
		m := base
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpStrList(fpNetID.String()), IPAddresses: fpNullStr(), Ports: fpNullInt(), Domains: fpNullStr()}
		tf := fpObj(t, fpObj(t, fpJSON(t, fpExpand(t, m)), "destination"), "trafficFilter")
		if tf["type"] != "NETWORK" {
			t.Errorf("type = %v, want NETWORK", tf["type"])
		}
	})
	t.Run("ip_addresses", func(t *testing.T) {
		m := base
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpNullStr(), IPAddresses: fpStrList("1.1.1.1"), Ports: fpNullInt(), Domains: fpNullStr()}
		tf := fpObj(t, fpObj(t, fpJSON(t, fpExpand(t, m)), "destination"), "trafficFilter")
		if tf["type"] != "IP_ADDRESS" {
			t.Errorf("type = %v, want IP_ADDRESS", tf["type"])
		}
	})
	t.Run("ports", func(t *testing.T) {
		m := base
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpNullStr(), IPAddresses: fpNullStr(), Ports: fpIntList(443), Domains: fpNullStr()}
		tf := fpObj(t, fpObj(t, fpJSON(t, fpExpand(t, m)), "destination"), "trafficFilter")
		if tf["type"] != "PORT" {
			t.Errorf("type = %v, want PORT", tf["type"])
		}
	})
	t.Run("domains", func(t *testing.T) {
		m := base
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpNullStr(), IPAddresses: fpNullStr(), Ports: fpNullInt(), Domains: fpStrList("example.com", "ads.example.net")}
		tf := fpObj(t, fpObj(t, fpJSON(t, fpExpand(t, m)), "destination"), "trafficFilter")
		if tf["type"] != "DOMAIN" {
			t.Errorf("type = %v, want DOMAIN", tf["type"])
		}
		df := fpObj(t, tf, "domainFilter")
		if df["type"] != "DOMAINS" {
			t.Errorf("domainFilter.type = %v, want DOMAINS", df["type"])
		}
		doms, ok := df["domains"].([]any)
		if !ok || len(doms) != 2 || doms[0] != "example.com" {
			t.Errorf("domains = %v", df["domains"])
		}
	})
}

func TestFirewallPolicyExpandDestinationConflict(t *testing.T) {
	m := fpBaseModel()
	m.Destination = &firewallPolicyDestinationModel{
		ZoneID:      types.StringValue(fpDstZone.String()),
		NetworkIDs:  fpNullStr(),
		IPAddresses: fpStrList("1.1.1.1"),
		Ports:       fpNullInt(),
		Domains:     fpStrList("example.com"),
	}
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Conflicting destination traffic filters")
}

func TestFirewallPolicyExpandDestinationBadZone(t *testing.T) {
	m := fpBaseModel()
	m.Destination.ZoneID = types.StringValue("nope")
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Invalid destination zone_id")
}

func TestFirewallPolicyExpandDestinationMissing(t *testing.T) {
	m := fpBaseModel()
	m.Destination = nil
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Missing destination")
}

// --- expand: scalars, connection state, ipsec ---------------------------------

func TestFirewallPolicyExpandScalars(t *testing.T) {
	m := fpBaseModel()
	m.Description = types.StringValue("iot segmentation")
	m.LoggingEnabled = types.BoolValue(true)
	m.Enabled = types.BoolValue(false)
	m.IpsecFilter = types.StringValue("MATCH_NOT_ENCRYPTED")
	m.ConnectionStateFilter = fpStrList("NEW", "ESTABLISHED")

	body := fpExpand(t, m)
	if body.Description == nil || *body.Description != "iot segmentation" {
		t.Errorf("description = %v", body.Description)
	}
	if body.Enabled || !body.LoggingEnabled {
		t.Errorf("enabled=%v loggingEnabled=%v", body.Enabled, body.LoggingEnabled)
	}
	if body.IpsecFilter == nil || string(*body.IpsecFilter) != "MATCH_NOT_ENCRYPTED" {
		t.Errorf("ipsecFilter = %v", body.IpsecFilter)
	}
	if body.ConnectionStateFilter == nil || len(*body.ConnectionStateFilter) != 2 {
		t.Fatalf("connectionStateFilter = %v", body.ConnectionStateFilter)
	}
	if (*body.ConnectionStateFilter)[0] != "NEW" {
		t.Errorf("connectionStateFilter[0] = %v, want NEW", (*body.ConnectionStateFilter)[0])
	}
}

func TestFirewallPolicyExpandConnectionStateInvalid(t *testing.T) {
	m := fpBaseModel()
	m.ConnectionStateFilter = fpStrList("NEW", "BOGUS")
	_, diags := expandFirewallPolicy(context.Background(), m)
	fpWantErr(t, diags, "Invalid connection state")
}

// --- flatten ------------------------------------------------------------------

func TestFirewallPolicyFlattenRoundTrip(t *testing.T) {
	m := fpBaseModel()
	m.Description = types.StringValue("desc")
	m.IpsecFilter = types.StringValue("MATCH_ENCRYPTED")
	m.ConnectionStateFilter = fpStrList("NEW", "RELATED")
	body := fpExpand(t, m)

	id := uuid.New()
	got, diags := flattenFirewallPolicy(context.Background(), fpEcho(id, body))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if got.ID.ValueString() != id.String() {
		t.Errorf("id = %q, want %q", got.ID.ValueString(), id)
	}
	if got.Name.ValueString() != "allow-iot-to-lan" {
		t.Errorf("name = %q", got.Name.ValueString())
	}
	if got.Index.ValueInt64() != 7 {
		t.Errorf("index = %d, want 7", got.Index.ValueInt64())
	}
	if got.Origin.ValueString() != "USER_DEFINED" {
		t.Errorf("origin = %q, want USER_DEFINED", got.Origin.ValueString())
	}
	if got.Description.ValueString() != "desc" {
		t.Errorf("description = %q", got.Description.ValueString())
	}
	if got.IpsecFilter.ValueString() != "MATCH_ENCRYPTED" {
		t.Errorf("ipsecFilter = %q", got.IpsecFilter.ValueString())
	}
	var states []string
	if d := got.ConnectionStateFilter.ElementsAs(context.Background(), &states, false); d.HasError() {
		t.Fatalf("states: %v", d)
	}
	if len(states) != 2 || states[0] != "NEW" || states[1] != "RELATED" {
		t.Errorf("connection states = %v", states)
	}
	if got.Action == nil || got.Action.Type.ValueString() != "ALLOW" {
		t.Errorf("action = %+v", got.Action)
	}
	if got.IPProtocolScope == nil || got.IPProtocolScope.IPVersion.ValueString() != "IPV4_AND_IPV6" {
		t.Errorf("scope = %+v", got.IPProtocolScope)
	}
	if got.Source == nil || got.Source.ZoneID.ValueString() != fpSrcZone.String() {
		t.Fatalf("source = %+v", got.Source)
	}
	var netIDs []string
	if d := got.Source.NetworkIDs.ElementsAs(context.Background(), &netIDs, false); d.HasError() {
		t.Fatalf("net ids: %v", d)
	}
	if len(netIDs) != 1 || netIDs[0] != fpNetID.String() {
		t.Errorf("source network ids = %v", netIDs)
	}
	if got.Destination == nil || got.Destination.ZoneID.ValueString() != fpDstZone.String() {
		t.Errorf("destination = %+v", got.Destination)
	}
}

func TestFirewallPolicyFlattenActionAllowReturn(t *testing.T) {
	m := fpBaseModel()
	m.Action = &firewallPolicyActionModel{Type: types.StringValue("ALLOW"), AllowReturnTraffic: types.BoolValue(true)}
	got, diags := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if !got.Action.AllowReturnTraffic.ValueBool() {
		t.Errorf("allow_return_traffic = %v, want true", got.Action.AllowReturnTraffic)
	}
}

func TestFirewallPolicyFlattenScopeProtocol(t *testing.T) {
	for _, tc := range []struct{ version, protocol string }{
		{"IPV4", "tcp"},
		{"IPV6", "icmpv6"},
		{"IPV4_AND_IPV6", "udp"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			m := fpBaseModel()
			m.IPProtocolScope = &firewallPolicyScopeModel{IPVersion: types.StringValue(tc.version), Protocol: types.StringValue(tc.protocol)}
			got, diags := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
			if diags.HasError() {
				t.Fatalf("flatten: %v", diags)
			}
			if got.IPProtocolScope.IPVersion.ValueString() != tc.version {
				t.Errorf("ip_version = %q, want %s", got.IPProtocolScope.IPVersion.ValueString(), tc.version)
			}
			if got.IPProtocolScope.Protocol.ValueString() != tc.protocol {
				t.Errorf("protocol = %q, want %s", got.IPProtocolScope.Protocol.ValueString(), tc.protocol)
			}
		})
	}
}

func TestFirewallPolicyFlattenSourceIPAndPorts(t *testing.T) {
	m := fpBaseModel()
	m.Source = &firewallPolicySourceModel{
		ZoneID:      types.StringValue(fpSrcZone.String()),
		NetworkIDs:  fpNullStr(),
		IPAddresses: fpStrList("10.0.0.5"),
		Ports:       fpNullInt(),
	}
	got, _ := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
	var ips []string
	if d := got.Source.IPAddresses.ElementsAs(context.Background(), &ips, false); d.HasError() {
		t.Fatalf("ips: %v", d)
	}
	if len(ips) != 1 || ips[0] != "10.0.0.5" {
		t.Errorf("source ip addresses = %v", ips)
	}
	if !got.Source.NetworkIDs.IsNull() {
		t.Errorf("network_ids should be null when ip_addresses set, got %v", got.Source.NetworkIDs)
	}

	m2 := fpBaseModel()
	m2.Source = &firewallPolicySourceModel{
		ZoneID:      types.StringValue(fpSrcZone.String()),
		NetworkIDs:  fpNullStr(),
		IPAddresses: fpNullStr(),
		Ports:       fpIntList(8080),
	}
	got2, _ := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m2)))
	var ports []int64
	if d := got2.Source.Ports.ElementsAs(context.Background(), &ports, false); d.HasError() {
		t.Fatalf("ports: %v", d)
	}
	if len(ports) != 1 || ports[0] != 8080 {
		t.Errorf("source ports = %v", ports)
	}
}

func TestFirewallPolicyFlattenDestinationDomains(t *testing.T) {
	m := fpBaseModel()
	m.Destination = &firewallPolicyDestinationModel{
		ZoneID:      types.StringValue(fpDstZone.String()),
		NetworkIDs:  fpNullStr(),
		IPAddresses: fpNullStr(),
		Ports:       fpNullInt(),
		Domains:     fpStrList("example.com"),
	}
	got, _ := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
	var doms []string
	if d := got.Destination.Domains.ElementsAs(context.Background(), &doms, false); d.HasError() {
		t.Fatalf("domains: %v", d)
	}
	if len(doms) != 1 || doms[0] != "example.com" {
		t.Errorf("destination domains = %v", doms)
	}
}

func TestFirewallPolicyFlattenDestinationVariants(t *testing.T) {
	dstZone := types.StringValue(fpDstZone.String())
	otherNet := uuid.New()

	t.Run("network", func(t *testing.T) {
		m := fpBaseModel()
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpStrList(otherNet.String()), IPAddresses: fpNullStr(), Ports: fpNullInt(), Domains: fpNullStr()}
		got, _ := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
		var ids []string
		if d := got.Destination.NetworkIDs.ElementsAs(context.Background(), &ids, false); d.HasError() {
			t.Fatalf("ids: %v", d)
		}
		if len(ids) != 1 || ids[0] != otherNet.String() {
			t.Errorf("destination network ids = %v", ids)
		}
	})
	t.Run("ip_addresses", func(t *testing.T) {
		m := fpBaseModel()
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpNullStr(), IPAddresses: fpStrList("8.8.8.8"), Ports: fpNullInt(), Domains: fpNullStr()}
		got, _ := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
		var ips []string
		if d := got.Destination.IPAddresses.ElementsAs(context.Background(), &ips, false); d.HasError() {
			t.Fatalf("ips: %v", d)
		}
		if len(ips) != 1 || ips[0] != "8.8.8.8" {
			t.Errorf("destination ip addresses = %v", ips)
		}
	})
	t.Run("ports", func(t *testing.T) {
		m := fpBaseModel()
		m.Destination = &firewallPolicyDestinationModel{ZoneID: dstZone, NetworkIDs: fpNullStr(), IPAddresses: fpNullStr(), Ports: fpIntList(123), Domains: fpNullStr()}
		got, _ := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
		var ports []int64
		if d := got.Destination.Ports.ElementsAs(context.Background(), &ports, false); d.HasError() {
			t.Fatalf("ports: %v", d)
		}
		if len(ports) != 1 || ports[0] != 123 {
			t.Errorf("destination ports = %v", ports)
		}
	})
}

func TestFirewallPolicyFlattenNoFilters(t *testing.T) {
	// Zone-only policy: no traffic filters on either side.
	m := fpBaseModel()
	m.Source = &firewallPolicySourceModel{ZoneID: types.StringValue(fpSrcZone.String()), NetworkIDs: fpNullStr(), IPAddresses: fpNullStr(), Ports: fpNullInt()}
	got, diags := flattenFirewallPolicy(context.Background(), fpEcho(uuid.New(), fpExpand(t, m)))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if !got.Source.NetworkIDs.IsNull() || !got.Source.IPAddresses.IsNull() || !got.Source.Ports.IsNull() {
		t.Errorf("zone-only source should have null filters, got %+v", got.Source)
	}
	if got.Description.ValueString() != "" || !got.Description.IsNull() {
		t.Errorf("description should be null, got %v", got.Description)
	}
}

// TestFirewallPolicyFlattenDeferredVariants proves the read path degrades to
// null (rather than crashing) for traffic-filter variants this provider does
// not model yet, e.g. a policy created in the UI with a region matcher or a
// shared traffic-matching-list. The controller can genuinely return these.
func TestFirewallPolicyFlattenDeferredVariants(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics

	if !fpFlattenIPAddresses(ctx, nil, &diags).IsNull() {
		t.Error("nil ip address filter should flatten to null")
	}
	if !fpFlattenPorts(ctx, nil, &diags).IsNull() {
		t.Error("nil port filter should flatten to null")
	}

	var ipf official.FirewallPolicyIPAddressFilter
	if err := ipf.FromFirewallPolicyIpAddressTrafficMatchingListFilter(
		official.FirewallPolicyIpAddressTrafficMatchingListFilter{Type: "TRAFFIC_MATCHING_LIST"}); err != nil {
		t.Fatalf("build deferred ip filter: %v", err)
	}
	if !fpFlattenIPAddresses(ctx, &ipf, &diags).IsNull() {
		t.Error("traffic-matching-list ip filter should flatten to null (deferred)")
	}

	var pf official.FirewallPolicyPortFilter
	if err := pf.FromFirewallPolicyPortReferenceFilter(
		official.FirewallPolicyPortReferenceFilter{Type: "TRAFFIC_MATCHING_LIST"}); err != nil {
		t.Fatalf("build deferred port filter: %v", err)
	}
	if !fpFlattenPorts(ctx, &pf, &diags).IsNull() {
		t.Error("traffic-matching-list port filter should flatten to null (deferred)")
	}

	// A source/destination carrying a deferred REGION variant: zone preserved,
	// every modeled filter list null.
	var srcTF official.FirewallPolicySourceTrafficFilter
	if err := srcTF.FromFirewallPolicySourceRegionFilter(official.FirewallPolicySourceRegionFilter{Type: "REGION"}); err != nil {
		t.Fatalf("build region source: %v", err)
	}
	src := flattenFirewallPolicySource(ctx, official.FirewallPolicySource{ZoneId: fpSrcZone, TrafficFilter: &srcTF}, &diags)
	if src.ZoneID.ValueString() != fpSrcZone.String() {
		t.Errorf("region source zone = %q", src.ZoneID.ValueString())
	}
	if !src.NetworkIDs.IsNull() || !src.IPAddresses.IsNull() || !src.Ports.IsNull() {
		t.Errorf("region source should have null modeled filters, got %+v", src)
	}

	var dstTF official.FirewallPolicyDestinationTrafficFilter
	if err := dstTF.FromFirewallPolicyDestinationRegionFilter(official.FirewallPolicyDestinationRegionFilter{Type: "REGION"}); err != nil {
		t.Fatalf("build region destination: %v", err)
	}
	dst := flattenFirewallPolicyDestination(ctx, official.FirewallPolicyDestination{ZoneId: fpDstZone, TrafficFilter: &dstTF}, &diags)
	if !dst.NetworkIDs.IsNull() || !dst.IPAddresses.IsNull() || !dst.Ports.IsNull() || !dst.Domains.IsNull() {
		t.Errorf("region destination should have null modeled filters, got %+v", dst)
	}
	if diags.HasError() {
		t.Fatalf("deferred-variant flatten should not error: %v", diags)
	}
}

// --- Create -------------------------------------------------------------------

func TestFirewallPolicyCreateReadOnly(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{}, testutil.ReadOnly())
	var resp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{}, &resp)
	fpWantErr(t, resp.Diagnostics, "read-only")
}

func TestFirewallPolicyCreateBadPlan(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	empty := fpSchema(t)
	req := resource.CreateRequest{Plan: tfsdk.Plan{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "not-an-object")}}
	resp := resource.CreateResponse{State: fpSchema(t)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected plan.Get to fail on mistyped raw value")
	}
}

func TestFirewallPolicyCreateExpandError(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	m := fpBaseModel()
	m.Source.ZoneID = types.StringValue("not-a-uuid")
	resp := resource.CreateResponse{State: fpSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: fpPlan(t, m)}, &resp)
	fpWantErr(t, resp.Diagnostics, "Invalid source zone_id")
}

func TestFirewallPolicyCreateAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		CreatePolicyFunc: func(_ context.Context, site uuid.UUID, body official.FirewallPolicyCreateOrUpdate) (*official.FirewallPolicy, error) {
			if site != testutil.SiteID {
				t.Errorf("site = %s, want %s", site, testutil.SiteID)
			}
			if body.Name != "allow-iot-to-lan" {
				t.Errorf("body name = %q", body.Name)
			}
			return nil, errors.New("controller exploded")
		},
	}
	r := fpResource(t, fw)
	resp := resource.CreateResponse{State: fpSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: fpPlan(t, fpBaseModel())}, &resp)
	fpWantErr(t, resp.Diagnostics, "Failed to create firewall policy")
}

func TestFirewallPolicyCreateOK(t *testing.T) {
	policyID := uuid.New()
	fw := &official.FirewallClientMock{
		CreatePolicyFunc: func(_ context.Context, _ uuid.UUID, body official.FirewallPolicyCreateOrUpdate) (*official.FirewallPolicy, error) {
			return fpEcho(policyID, body), nil
		},
	}
	r := fpResource(t, fw)
	resp := resource.CreateResponse{State: fpSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: fpPlan(t, fpBaseModel())}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	var got firewallPolicyModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != policyID.String() {
		t.Errorf("id = %q, want %q", got.ID.ValueString(), policyID)
	}
	if got.Action.Type.ValueString() != "ALLOW" {
		t.Errorf("action type = %q", got.Action.Type.ValueString())
	}
}

// --- Read ---------------------------------------------------------------------

func TestFirewallPolicyReadBadState(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	empty := fpSchema(t)
	req := resource.ReadRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Bool, true)}}
	resp := resource.ReadResponse{State: fpSchema(t)}
	r.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestFirewallPolicyReadBadID(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	m := fpBaseModel()
	m.ID = types.StringValue("not-a-uuid")
	st := fpState(t, m)
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	fpWantErr(t, resp.Diagnostics, "Invalid firewall policy id")
}

func TestFirewallPolicyReadNotFoundRemoves(t *testing.T) {
	fw := &official.FirewallClientMock{
		GetPolicyFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallPolicy, error) {
			return nil, fmt.Errorf("policy lookup: %w", unifi.ErrNotFound)
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(uuid.New().String())
	st := fpState(t, m)
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("not-found read should not error: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state should be removed when the policy is gone")
	}
}

func TestFirewallPolicyReadAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		GetPolicyFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.FirewallPolicy, error) {
			return nil, errors.New("boom")
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(uuid.New().String())
	st := fpState(t, m)
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	fpWantErr(t, resp.Diagnostics, "Failed to read firewall policy")
}

func TestFirewallPolicyReadOK(t *testing.T) {
	policyID := uuid.New()
	body := fpExpand(t, fpBaseModel())
	fw := &official.FirewallClientMock{
		GetPolicyFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) (*official.FirewallPolicy, error) {
			if site != testutil.SiteID || id != policyID {
				t.Errorf("get policy called with site %s id %s", site, id)
			}
			echoed := fpEcho(policyID, body)
			echoed.Name = "renamed-policy"
			return echoed, nil
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(policyID.String())
	st := fpState(t, m)
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got firewallPolicyModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.Name.ValueString() != "renamed-policy" {
		t.Errorf("name = %q, want renamed-policy (refreshed)", got.Name.ValueString())
	}
}

// --- Update -------------------------------------------------------------------

func TestFirewallPolicyUpdateReadOnly(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{}, testutil.ReadOnly())
	var resp resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{}, &resp)
	fpWantErr(t, resp.Diagnostics, "read-only")
}

func TestFirewallPolicyUpdateBadID(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	m := fpBaseModel()
	m.ID = types.StringValue("not-a-uuid")
	resp := resource.UpdateResponse{State: fpSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: fpPlan(t, m)}, &resp)
	fpWantErr(t, resp.Diagnostics, "Invalid firewall policy id")
}

func TestFirewallPolicyUpdateAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		UpdatePolicyFunc: func(context.Context, uuid.UUID, uuid.UUID, official.FirewallPolicyCreateOrUpdate) (*official.FirewallPolicy, error) {
			return nil, errors.New("boom")
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(uuid.New().String())
	resp := resource.UpdateResponse{State: fpSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: fpPlan(t, m)}, &resp)
	fpWantErr(t, resp.Diagnostics, "Failed to update firewall policy")
}

func TestFirewallPolicyUpdateOK(t *testing.T) {
	policyID := uuid.New()
	fw := &official.FirewallClientMock{
		UpdatePolicyFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID, body official.FirewallPolicyCreateOrUpdate) (*official.FirewallPolicy, error) {
			if site != testutil.SiteID || id != policyID {
				t.Errorf("update policy called with site %s id %s", site, id)
			}
			return fpEcho(policyID, body), nil
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(policyID.String())
	m.Name = types.StringValue("updated-policy")
	m.Action = &firewallPolicyActionModel{Type: types.StringValue("BLOCK"), AllowReturnTraffic: types.BoolNull()}
	resp := resource.UpdateResponse{State: fpSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: fpPlan(t, m)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update: %v", resp.Diagnostics)
	}
	var got firewallPolicyModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.Name.ValueString() != "updated-policy" {
		t.Errorf("name = %q, want updated-policy", got.Name.ValueString())
	}
	if got.Action.Type.ValueString() != "BLOCK" {
		t.Errorf("action type = %q, want BLOCK", got.Action.Type.ValueString())
	}
}

// --- Delete -------------------------------------------------------------------

func TestFirewallPolicyDeleteGuards(t *testing.T) {
	for name, opt := range map[string]testutil.Opt{
		"read_only":          testutil.ReadOnly(),
		"destroy_protection": testutil.DestroyProtection(),
	} {
		t.Run(name, func(t *testing.T) {
			r := fpResource(t, &official.FirewallClientMock{}, opt)
			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{}, &resp)
			fpWantErr(t, resp.Diagnostics, "Delete blocked")
		})
	}
}

func TestFirewallPolicyDeleteBadState(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	empty := fpSchema(t)
	req := resource.DeleteRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "junk")}}
	var resp resource.DeleteResponse
	r.Delete(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestFirewallPolicyDeleteBadID(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	m := fpBaseModel()
	m.ID = types.StringValue("not-a-uuid")
	st := fpState(t, m)
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	fpWantErr(t, resp.Diagnostics, "Invalid firewall policy id")
}

func TestFirewallPolicyDeleteAPIError(t *testing.T) {
	fw := &official.FirewallClientMock{
		DeletePolicyFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
			return errors.New("policy is system-defined")
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(uuid.New().String())
	st := fpState(t, m)
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	fpWantErr(t, resp.Diagnostics, "Failed to delete firewall policy")
}

func TestFirewallPolicyDeleteNotFoundIsSuccess(t *testing.T) {
	fw := &official.FirewallClientMock{
		DeletePolicyFunc: func(context.Context, uuid.UUID, uuid.UUID) error {
			return fmt.Errorf("delete: %w", unifi.ErrNotFound)
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(uuid.New().String())
	st := fpState(t, m)
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("not-found delete should succeed: %v", resp.Diagnostics)
	}
}

func TestFirewallPolicyDeleteOK(t *testing.T) {
	policyID := uuid.New()
	fw := &official.FirewallClientMock{
		DeletePolicyFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) error {
			if site != testutil.SiteID || id != policyID {
				t.Errorf("delete policy called with site %s id %s", site, id)
			}
			return nil
		},
	}
	r := fpResource(t, fw)
	m := fpBaseModel()
	m.ID = types.StringValue(policyID.String())
	st := fpState(t, m)
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
}

// --- ImportState --------------------------------------------------------------

func TestFirewallPolicyImportState(t *testing.T) {
	r := fpResource(t, &official.FirewallClientMock{})
	id := uuid.New().String()
	resp := resource.ImportStateResponse{State: fpSchema(t)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import: %v", resp.Diagnostics)
	}
	var got firewallPolicyModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != id {
		t.Errorf("imported id = %q, want %q", got.ID.ValueString(), id)
	}
}
