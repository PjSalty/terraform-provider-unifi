package resources

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/filipowm/go-unifi/v2/unifi/official"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestExpandHotspotVoucherRequired guards the required-field mapping and that
// unset optionals stay absent (nil pointer + omitempty) rather than being sent
// as zeros, which the controller would read as a 0-limit instead of unlimited.
func TestExpandHotspotVoucherRequired(t *testing.T) {
	body := expandHotspotVoucher(hotspotVoucherModel{
		Name:             types.StringValue("Lobby"),
		Count:            types.Int32Value(5),
		TimeLimitMinutes: types.Int64Value(120),
		// all rate/usage/guest limits null
		AuthorizedGuestLimit: types.Int64Null(),
		DataUsageLimitMBytes: types.Int64Null(),
		RxRateLimitKbps:      types.Int64Null(),
		TxRateLimitKbps:      types.Int64Null(),
	})
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["name"] != "Lobby" {
		t.Errorf("name = %v, want Lobby", got["name"])
	}
	if got["count"] != float64(5) {
		t.Errorf("count = %v, want 5", got["count"])
	}
	if got["timeLimitMinutes"] != float64(120) {
		t.Errorf("timeLimitMinutes = %v, want 120", got["timeLimitMinutes"])
	}
	for _, k := range []string{"authorizedGuestLimit", "dataUsageLimitMBytes", "rxRateLimitKbps", "txRateLimitKbps"} {
		if _, present := got[k]; present {
			t.Errorf("optional %q should be omitted when null, got %v", k, got[k])
		}
	}
}

// TestExpandHotspotVoucherOptionals proves set optionals reach the body.
func TestExpandHotspotVoucherOptionals(t *testing.T) {
	body := expandHotspotVoucher(hotspotVoucherModel{
		Name:                 types.StringValue("Conf"),
		Count:                types.Int32Null(),
		TimeLimitMinutes:     types.Int64Value(60),
		AuthorizedGuestLimit: types.Int64Value(3),
		DataUsageLimitMBytes: types.Int64Value(1024),
		RxRateLimitKbps:      types.Int64Value(5000),
		TxRateLimitKbps:      types.Int64Value(2000),
	})
	if body.Count != nil {
		t.Errorf("count should be nil when null, got %v", *body.Count)
	}
	if body.AuthorizedGuestLimit == nil || *body.AuthorizedGuestLimit != 3 {
		t.Errorf("authorizedGuestLimit = %v, want 3", body.AuthorizedGuestLimit)
	}
	if body.DataUsageLimitMBytes == nil || *body.DataUsageLimitMBytes != 1024 {
		t.Errorf("dataUsageLimitMBytes = %v, want 1024", body.DataUsageLimitMBytes)
	}
	if body.RxRateLimitKbps == nil || *body.RxRateLimitKbps != 5000 {
		t.Errorf("rxRateLimitKbps = %v, want 5000", body.RxRateLimitKbps)
	}
	if body.TxRateLimitKbps == nil || *body.TxRateLimitKbps != 2000 {
		t.Errorf("txRateLimitKbps = %v, want 2000", body.TxRateLimitKbps)
	}
}

// TestFlattenHotspotVouchers checks the resource id is the first voucher's
// UUID, count reflects the batch size, the computed list carries every code,
// and optional/time fields round-trip including null.
func TestFlattenHotspotVouchers(t *testing.T) {
	id0, id1 := uuid.New(), uuid.New()
	created := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	expires := created.Add(2 * time.Hour)
	limit := int64(10)
	vouchers := []official.HotspotVoucherDetails{
		{
			Id:                   id0,
			Code:                 "1111-2222",
			Name:                 "Lobby",
			Expired:              false,
			AuthorizedGuestCount: 0,
			AuthorizedGuestLimit: &limit,
			TimeLimitMinutes:     120,
			CreatedAt:            created,
			ExpiresAt:            &expires,
			// ActivatedAt nil -> null
		},
		{
			Id:                   id1,
			Code:                 "3333-4444",
			Name:                 "Lobby",
			Expired:              true,
			AuthorizedGuestCount: 1,
			TimeLimitMinutes:     120,
			CreatedAt:            created,
			// no AuthorizedGuestLimit -> null, no ExpiresAt -> null
		},
	}
	in := hotspotVoucherModel{Name: types.StringValue("Lobby"), TimeLimitMinutes: types.Int64Value(120)}
	out, diags := flattenHotspotVouchers(context.Background(), in, vouchers)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if out.ID.ValueString() != id0.String() {
		t.Errorf("id = %q, want first voucher %q", out.ID.ValueString(), id0.String())
	}
	if out.Count.ValueInt32() != 2 {
		t.Errorf("count = %d, want 2", out.Count.ValueInt32())
	}
	if out.AuthorizedGuestLimit.ValueInt64() != 10 {
		t.Errorf("authorized_guest_limit = %d, want 10", out.AuthorizedGuestLimit.ValueInt64())
	}
	if !out.DataUsageLimitMBytes.IsNull() {
		t.Errorf("data_usage_limit_mbytes should be null, got %v", out.DataUsageLimitMBytes)
	}

	var elems []voucherObjModel
	if d := out.Vouchers.ElementsAs(context.Background(), &elems, false); d.HasError() {
		t.Fatalf("ElementsAs: %v", d)
	}
	if len(elems) != 2 {
		t.Fatalf("vouchers len = %d, want 2", len(elems))
	}
	if elems[0].Code.ValueString() != "1111-2222" {
		t.Errorf("voucher[0].code = %q, want 1111-2222", elems[0].Code.ValueString())
	}
	if elems[0].CreatedAt.ValueString() != created.Format(time.RFC3339) {
		t.Errorf("voucher[0].created_at = %q, want %q", elems[0].CreatedAt.ValueString(), created.Format(time.RFC3339))
	}
	if elems[0].ExpiresAt.ValueString() != expires.Format(time.RFC3339) {
		t.Errorf("voucher[0].expires_at = %q, want %q", elems[0].ExpiresAt.ValueString(), expires.Format(time.RFC3339))
	}
	if !elems[0].ActivatedAt.IsNull() {
		t.Errorf("voucher[0].activated_at should be null, got %v", elems[0].ActivatedAt)
	}
	if !elems[1].ExpiresAt.IsNull() {
		t.Errorf("voucher[1].expires_at should be null, got %v", elems[1].ExpiresAt)
	}
	if !elems[1].Expired.ValueBool() {
		t.Error("voucher[1].expired should be true")
	}
}

// TestVoucherIDsFromState ensures the delete/read paths recover every UUID from
// the computed list in state.
func TestVoucherIDsFromState(t *testing.T) {
	id0, id1 := uuid.New(), uuid.New()
	in := hotspotVoucherModel{Name: types.StringValue("x"), TimeLimitMinutes: types.Int64Value(60)}
	model, diags := flattenHotspotVouchers(context.Background(), in, []official.HotspotVoucherDetails{
		{Id: id0, Code: "a", Name: "x", TimeLimitMinutes: 60, CreatedAt: time.Now()},
		{Id: id1, Code: "b", Name: "x", TimeLimitMinutes: 60, CreatedAt: time.Now()},
	})
	if diags.HasError() {
		t.Fatalf("flatten diags: %v", diags)
	}
	ids, d := voucherIDsFromState(context.Background(), model)
	if d.HasError() {
		t.Fatalf("voucherIDsFromState: %v", d)
	}
	if len(ids) != 2 || ids[0] != id0 || ids[1] != id1 {
		t.Errorf("ids = %v, want [%s %s]", ids, id0, id1)
	}
}
