package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

func hvSchema(t *testing.T) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	(&hotspotVoucherResource{}).Schema(context.Background(), resource.SchemaRequest{}, &sr)
	if sr.Diagnostics.HasError() {
		t.Fatalf("schema: %v", sr.Diagnostics)
	}
	return tfsdk.State{
		Schema: sr.Schema,
		Raw:    tftypes.NewValue(sr.Schema.Type().TerraformType(context.Background()), nil),
	}
}

func hvResource(t *testing.T, hs *official.HotspotClientMock, opts ...testutil.Opt) *hotspotVoucherResource {
	t.Helper()
	r, ok := NewHotspotVoucherResource().(*hotspotVoucherResource)
	if !ok {
		t.Fatal("NewHotspotVoucherResource returned the wrong type")
	}
	oc := &official.ClientMock{HotspotFunc: func() official.HotspotClient { return hs }}
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(oc, opts...)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("configure: %v", resp.Diagnostics)
	}
	return r
}

func hvPlan(t *testing.T, m hotspotVoucherModel) tfsdk.Plan {
	t.Helper()
	empty := hvSchema(t)
	p := tfsdk.Plan(empty)
	if d := p.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building plan: %v", d)
	}
	return p
}

func hvState(t *testing.T, m hotspotVoucherModel) tfsdk.State {
	t.Helper()
	s := hvSchema(t)
	if d := s.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("building state: %v", d)
	}
	return s
}

func hvWantErr(t *testing.T, diags diag.Diagnostics, want string) {
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

// hvVoucherList builds the computed vouchers list from raw id strings, so the
// invalid-UUID paths can be represented too.
func hvVoucherList(t *testing.T, ids ...string) types.List {
	t.Helper()
	elems := make([]voucherObjModel, 0, len(ids))
	for _, id := range ids {
		elems = append(elems, voucherObjModel{
			ID:                   types.StringValue(id),
			Code:                 types.StringValue("0000-1111"),
			Name:                 types.StringValue("guest-pass"),
			Expired:              types.BoolValue(false),
			AuthorizedGuestCount: types.Int64Value(0),
			CreatedAt:            types.StringValue("2026-01-02T15:04:05Z"),
			ExpiresAt:            types.StringNull(),
			ActivatedAt:          types.StringNull(),
		})
	}
	l, d := types.ListValueFrom(context.Background(), voucherObjectType, elems)
	if d.HasError() {
		t.Fatalf("building voucher list: %v", d)
	}
	return l
}

// hvModel returns a fully-populated state model for the given resource id and
// voucher list.
func hvModel(id string, vouchers types.List) hotspotVoucherModel {
	return hotspotVoucherModel{
		ID:                   types.StringValue(id),
		Name:                 types.StringValue("guest-pass"),
		Count:                types.Int32Value(1),
		TimeLimitMinutes:     types.Int64Value(60),
		AuthorizedGuestLimit: types.Int64Null(),
		DataUsageLimitMBytes: types.Int64Null(),
		RxRateLimitKbps:      types.Int64Null(),
		TxRateLimitKbps:      types.Int64Null(),
		Vouchers:             vouchers,
	}
}

func hvDetails(id uuid.UUID) *official.HotspotVoucherDetails {
	return &official.HotspotVoucherDetails{
		Id:               id,
		Code:             "2222-3333",
		Name:             "guest-pass",
		TimeLimitMinutes: 60,
		CreatedAt:        time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
	}
}

// hvBreakVoucherType swaps one attribute of the package-level voucher object
// type to a mismatching type for the duration of the test, so the otherwise
// infallible types.ListValueFrom in flattenHotspotVouchers genuinely fails and
// the flatten-error branches run.
func hvBreakVoucherType(t *testing.T) {
	t.Helper()
	orig := voucherObjectType.AttrTypes
	broken := make(map[string]attr.Type, len(orig))
	for k, v := range orig {
		broken[k] = v
	}
	broken["expired"] = types.Int64Type
	voucherObjectType.AttrTypes = broken
	t.Cleanup(func() { voucherObjectType.AttrTypes = orig })
}

// --- Metadata / Schema / Configure --------------------------------------------

func TestHotspotVoucherMetadata(t *testing.T) {
	var resp resource.MetadataResponse
	(&hotspotVoucherResource{}).Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "unifi"}, &resp)
	if resp.TypeName != "unifi_hotspot_voucher" {
		t.Errorf("type name = %q, want unifi_hotspot_voucher", resp.TypeName)
	}
}

func TestHotspotVoucherSchemaAttributes(t *testing.T) {
	s := hvSchema(t)
	for _, name := range []string{
		"id", "name", "quantity", "time_limit_minutes", "authorized_guest_limit",
		"data_usage_limit_mbytes", "rx_rate_limit_kbps", "tx_rate_limit_kbps", "vouchers",
	} {
		if _, ok := s.Schema.GetAttributes()[name]; !ok {
			t.Errorf("schema missing attribute %q", name)
		}
	}
}

func TestHotspotVoucherConfigure(t *testing.T) {
	r := &hotspotVoucherResource{}

	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &resp)
	if resp.Diagnostics.HasError() || r.data != nil {
		t.Errorf("nil provider data should be a no-op, diags %v", resp.Diagnostics)
	}

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "bogus"}, &resp)
	hvWantErr(t, resp.Diagnostics, "Unexpected provider data")

	resp = resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: testutil.Data(&official.ClientMock{})}, &resp)
	if resp.Diagnostics.HasError() || r.data == nil {
		t.Errorf("configure with provider data failed: %v", resp.Diagnostics)
	}
}

// --- Create --------------------------------------------------------------------

func TestHotspotVoucherCreateReadOnly(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{}, testutil.ReadOnly())
	var resp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{}, &resp)
	hvWantErr(t, resp.Diagnostics, "read-only")
}

func TestHotspotVoucherCreateBadPlan(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	empty := hvSchema(t)
	req := resource.CreateRequest{Plan: tfsdk.Plan{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.String, "junk")}}
	resp := resource.CreateResponse{State: hvSchema(t)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected plan.Get to fail on mistyped raw value")
	}
}

func hvCreatePlan(t *testing.T, quantity int32) tfsdk.Plan {
	t.Helper()
	m := hvModel("", types.ListUnknown(voucherObjectType))
	m.ID = types.StringUnknown()
	m.Count = types.Int32Value(quantity)
	return hvPlan(t, m)
}

func TestHotspotVoucherCreateAPIError(t *testing.T) {
	hs := &official.HotspotClientMock{
		CreateVouchersFunc: func(_ context.Context, site uuid.UUID, body official.HotspotVoucherCreationRequest) (*official.VoucherCreationResult, error) {
			if site != testutil.SiteID {
				t.Errorf("site = %s, want %s", site, testutil.SiteID)
			}
			if body.Name != "guest-pass" || body.TimeLimitMinutes != 60 {
				t.Errorf("body = %+v, want guest-pass/60", body)
			}
			if body.Count == nil || *body.Count != 2 {
				t.Errorf("count = %v, want 2", body.Count)
			}
			return nil, errors.New("boom")
		},
	}
	r := hvResource(t, hs)
	resp := resource.CreateResponse{State: hvSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: hvCreatePlan(t, 2)}, &resp)
	hvWantErr(t, resp.Diagnostics, "Failed to create hotspot vouchers")
}

func TestHotspotVoucherCreateNoVouchers(t *testing.T) {
	for name, result := range map[string]*official.VoucherCreationResult{
		"nil_set":   {Vouchers: nil},
		"empty_set": {Vouchers: &[]official.HotspotVoucherDetails{}},
	} {
		t.Run(name, func(t *testing.T) {
			hs := &official.HotspotClientMock{
				CreateVouchersFunc: func(context.Context, uuid.UUID, official.HotspotVoucherCreationRequest) (*official.VoucherCreationResult, error) {
					return result, nil
				},
			}
			r := hvResource(t, hs)
			resp := resource.CreateResponse{State: hvSchema(t)}
			r.Create(context.Background(), resource.CreateRequest{Plan: hvCreatePlan(t, 1)}, &resp)
			hvWantErr(t, resp.Diagnostics, "returned no vouchers")
		})
	}
}

func TestHotspotVoucherCreateFlattenError(t *testing.T) {
	hs := &official.HotspotClientMock{
		CreateVouchersFunc: func(context.Context, uuid.UUID, official.HotspotVoucherCreationRequest) (*official.VoucherCreationResult, error) {
			return &official.VoucherCreationResult{Vouchers: &[]official.HotspotVoucherDetails{*hvDetails(uuid.New())}}, nil
		},
	}
	r := hvResource(t, hs)
	plan := hvCreatePlan(t, 1)
	resp := resource.CreateResponse{State: hvSchema(t)}
	hvBreakVoucherType(t)
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected flattening to fail with a broken voucher object type")
	}
}

func TestHotspotVoucherCreateOK(t *testing.T) {
	id0, id1 := uuid.New(), uuid.New()
	hs := &official.HotspotClientMock{
		CreateVouchersFunc: func(context.Context, uuid.UUID, official.HotspotVoucherCreationRequest) (*official.VoucherCreationResult, error) {
			return &official.VoucherCreationResult{Vouchers: &[]official.HotspotVoucherDetails{*hvDetails(id0), *hvDetails(id1)}}, nil
		},
	}
	r := hvResource(t, hs)
	resp := resource.CreateResponse{State: hvSchema(t)}
	r.Create(context.Background(), resource.CreateRequest{Plan: hvCreatePlan(t, 2)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	var got hotspotVoucherModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != id0.String() {
		t.Errorf("id = %q, want first voucher %q", got.ID.ValueString(), id0)
	}
	if got.Count.ValueInt32() != 2 {
		t.Errorf("quantity = %d, want 2", got.Count.ValueInt32())
	}
	var elems []voucherObjModel
	if d := got.Vouchers.ElementsAs(context.Background(), &elems, false); d.HasError() {
		t.Fatalf("vouchers: %v", d)
	}
	if len(elems) != 2 || elems[1].ID.ValueString() != id1.String() {
		t.Errorf("vouchers = %v, want 2 entries ending %s", elems, id1)
	}
}

// --- Read ----------------------------------------------------------------------

func TestHotspotVoucherReadBadState(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	empty := hvSchema(t)
	req := resource.ReadRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Bool, false)}}
	resp := resource.ReadResponse{State: hvSchema(t)}
	r.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestHotspotVoucherReadIDsError(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	st := hvState(t, hvModel(uuid.New().String(), hvVoucherList(t, "not-a-uuid")))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	hvWantErr(t, resp.Diagnostics, "Invalid hotspot voucher id")
}

func TestHotspotVoucherReadBadPrimaryID(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	st := hvState(t, hvModel("not-a-uuid", hvVoucherList(t, uuid.New().String())))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	hvWantErr(t, resp.Diagnostics, "Invalid hotspot voucher id")
}

func TestHotspotVoucherReadPrimaryGoneRemoves(t *testing.T) {
	primary := uuid.New()
	hs := &official.HotspotClientMock{
		GetVoucherFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.HotspotVoucherDetails, error) {
			return nil, fmt.Errorf("voucher: %w", unifi.ErrNotFound)
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), hvVoucherList(t, primary.String())))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("primary-gone read should not error: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state should be removed when the primary voucher is gone")
	}
}

func TestHotspotVoucherReadSecondaryGoneKeeps(t *testing.T) {
	primary, secondary := uuid.New(), uuid.New()
	hs := &official.HotspotClientMock{
		GetVoucherFunc: func(_ context.Context, _ uuid.UUID, id uuid.UUID) (*official.HotspotVoucherDetails, error) {
			if id == secondary {
				return nil, unifi.ErrNotFound
			}
			return hvDetails(id), nil
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), hvVoucherList(t, primary.String(), secondary.String())))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got hotspotVoucherModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.Count.ValueInt32() != 1 {
		t.Errorf("quantity = %d, want 1 (consumed voucher dropped)", got.Count.ValueInt32())
	}
}

func TestHotspotVoucherReadAPIError(t *testing.T) {
	primary := uuid.New()
	hs := &official.HotspotClientMock{
		GetVoucherFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.HotspotVoucherDetails, error) {
			return nil, errors.New("boom")
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), hvVoucherList(t, primary.String())))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	hvWantErr(t, resp.Diagnostics, "Failed to read hotspot voucher")
}

// TestHotspotVoucherReadAllGoneRemoves drives the batch through a state whose
// voucher list no longer contains the primary id: every listed voucher is gone
// (non-primary not-founds are skipped), so the live set is empty and the
// resource is removed.
func TestHotspotVoucherReadAllGoneRemoves(t *testing.T) {
	primary, stray := uuid.New(), uuid.New()
	hs := &official.HotspotClientMock{
		GetVoucherFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.HotspotVoucherDetails, error) {
			return nil, unifi.ErrNotFound
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), hvVoucherList(t, stray.String())))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("all-gone read should not error: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state should be removed when no vouchers survive")
	}
}

// TestHotspotVoucherReadImportFallback reads with a null vouchers list (fresh
// import), falling back to the resource id alone.
func TestHotspotVoucherReadImportFallback(t *testing.T) {
	primary := uuid.New()
	hs := &official.HotspotClientMock{
		GetVoucherFunc: func(_ context.Context, _ uuid.UUID, id uuid.UUID) (*official.HotspotVoucherDetails, error) {
			if id != primary {
				t.Errorf("get voucher id = %s, want %s", id, primary)
			}
			return hvDetails(id), nil
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), types.ListNull(voucherObjectType)))
	resp := resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	var got hotspotVoucherModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != primary.String() || got.Count.ValueInt32() != 1 {
		t.Errorf("state = id %q count %d, want %s / 1", got.ID.ValueString(), got.Count.ValueInt32(), primary)
	}
}

func TestHotspotVoucherReadFlattenError(t *testing.T) {
	primary := uuid.New()
	hs := &official.HotspotClientMock{
		GetVoucherFunc: func(_ context.Context, _ uuid.UUID, id uuid.UUID) (*official.HotspotVoucherDetails, error) {
			return hvDetails(id), nil
		},
	}
	r := hvResource(t, hs)
	// Null vouchers list so state construction does not depend on the type we
	// are about to break.
	st := hvState(t, hvModel(primary.String(), types.ListNull(voucherObjectType)))
	resp := resource.ReadResponse{State: st}
	hvBreakVoucherType(t)
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected flattening to fail with a broken voucher object type")
	}
}

// --- Update --------------------------------------------------------------------

func TestHotspotVoucherUpdateReadOnly(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{}, testutil.ReadOnly())
	var resp resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{}, &resp)
	hvWantErr(t, resp.Diagnostics, "read-only")
}

func TestHotspotVoucherUpdateAlwaysFails(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	var resp resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{}, &resp)
	hvWantErr(t, resp.Diagnostics, "cannot be updated")
}

// --- Delete --------------------------------------------------------------------

func TestHotspotVoucherDeleteGuards(t *testing.T) {
	for name, opt := range map[string]testutil.Opt{
		"read_only":          testutil.ReadOnly(),
		"destroy_protection": testutil.DestroyProtection(),
	} {
		t.Run(name, func(t *testing.T) {
			r := hvResource(t, &official.HotspotClientMock{}, opt)
			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{}, &resp)
			hvWantErr(t, resp.Diagnostics, "Delete blocked")
		})
	}
}

func TestHotspotVoucherDeleteBadState(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	empty := hvSchema(t)
	req := resource.DeleteRequest{State: tfsdk.State{Schema: empty.Schema, Raw: tftypes.NewValue(tftypes.Number, 3)}}
	var resp resource.DeleteResponse
	r.Delete(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected state.Get to fail on mistyped raw value")
	}
}

func TestHotspotVoucherDeleteIDsError(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	st := hvState(t, hvModel(uuid.New().String(), hvVoucherList(t, "not-a-uuid")))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	hvWantErr(t, resp.Diagnostics, "Invalid hotspot voucher id")
}

func TestHotspotVoucherDeleteAPIError(t *testing.T) {
	primary := uuid.New()
	hs := &official.HotspotClientMock{
		DeleteVoucherFunc: func(context.Context, uuid.UUID, uuid.UUID) (*official.VoucherDeletionResults, error) {
			return nil, errors.New("boom")
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), hvVoucherList(t, primary.String())))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	hvWantErr(t, resp.Diagnostics, "Failed to delete hotspot voucher")
}

// TestHotspotVoucherDeleteOK deletes a two-voucher batch where one is already
// gone: the not-found is tolerated and the other delete succeeds.
func TestHotspotVoucherDeleteOK(t *testing.T) {
	primary, secondary := uuid.New(), uuid.New()
	deleted := make([]uuid.UUID, 0, 2)
	hs := &official.HotspotClientMock{
		DeleteVoucherFunc: func(_ context.Context, site uuid.UUID, id uuid.UUID) (*official.VoucherDeletionResults, error) {
			if site != testutil.SiteID {
				t.Errorf("site = %s, want %s", site, testutil.SiteID)
			}
			deleted = append(deleted, id)
			if id == secondary {
				return nil, fmt.Errorf("delete: %w", unifi.ErrNotFound)
			}
			return &official.VoucherDeletionResults{}, nil
		},
	}
	r := hvResource(t, hs)
	st := hvState(t, hvModel(primary.String(), hvVoucherList(t, primary.String(), secondary.String())))
	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
	if len(deleted) != 2 || deleted[0] != primary || deleted[1] != secondary {
		t.Errorf("deleted = %v, want [%s %s]", deleted, primary, secondary)
	}
}

// --- ImportState -----------------------------------------------------------------

func TestHotspotVoucherImportState(t *testing.T) {
	r := hvResource(t, &official.HotspotClientMock{})
	id := uuid.New().String()
	resp := resource.ImportStateResponse{State: hvSchema(t)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import: %v", resp.Diagnostics)
	}
	var got hotspotVoucherModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state get: %v", d)
	}
	if got.ID.ValueString() != id {
		t.Errorf("imported id = %q, want %q", got.ID.ValueString(), id)
	}
}

// --- helpers ---------------------------------------------------------------------

func TestVoucherIDsFromStateEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("null_list_invalid_id", func(t *testing.T) {
		_, diags := voucherIDsFromState(ctx, hvModel("not-a-uuid", types.ListNull(voucherObjectType)))
		hvWantErr(t, diags, "Invalid hotspot voucher id")
	})

	t.Run("unknown_list_falls_back_to_id", func(t *testing.T) {
		id := uuid.New()
		ids, diags := voucherIDsFromState(ctx, hvModel(id.String(), types.ListUnknown(voucherObjectType)))
		if diags.HasError() {
			t.Fatalf("diags: %v", diags)
		}
		if len(ids) != 1 || ids[0] != id {
			t.Errorf("ids = %v, want [%s]", ids, id)
		}
	})

	t.Run("elements_as_error", func(t *testing.T) {
		list := types.ListValueMust(voucherObjectType, []attr.Value{types.ObjectUnknown(voucherObjectType.AttrTypes)})
		_, diags := voucherIDsFromState(ctx, hvModel(uuid.New().String(), list))
		if !diags.HasError() {
			t.Fatal("expected ElementsAs to fail on an unknown element")
		}
	})
}
