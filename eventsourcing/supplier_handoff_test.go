package eventsourcing

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// NIAGA-546: the wire shape of the dropship hand-off is pinned here, the way
// order_payloads_test.go pins the order events. A renamed json tag on either
// side is exactly the silent drift that file records.
func TestSupplierHandoffEmitsExactlyTheseKeys(t *testing.T) {
	ship := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	p := SupplierHandoffPayload{
		HandoffID: "h1", MarketplaceOrderID: "m1", Platform: "shopee", PlatformOrderSN: "SN1", ShipBy: &ship,
		SupplierID: "s1", SupplierName: "Kilang A", SupplierEmail: "a@example.test",
		LabelObjectKey: "handoffs/h1/label.pdf", LabelFilename: "label-SN1.pdf",
		Lines: []SupplierHandoffLine{{SKU: "K-1", Name: "Kain", VariantName: "Biru", Quantity: 2}},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"handoff_id", "label_filename", "label_object_key", "lines", "marketplace_order_id",
		"platform", "platform_order_sn", "ship_by", "supplier_email", "supplier_id", "supplier_name"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v\nwant   %v", keys, want)
	}
	line := m["lines"].([]any)[0].(map[string]any)
	for _, k := range []string{"sku", "name", "variant_name", "quantity"} {
		if _, ok := line[k]; !ok {
			t.Errorf("line lacks %q: %v", k, line)
		}
	}

	var back SupplierHandoffPayload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, p) {
		t.Fatalf("round trip changed the payload:\n got %+v\nwant %+v", back, p)
	}
}

func TestSupplierHandoffCarriesNoBuyerContact(t *testing.T) {
	// The supplier gets the label and the lines, nothing more about the buyer.
	typ := reflect.TypeOf(SupplierHandoffPayload{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		for _, bad := range []string{"buyer", "customer", "phone", "address"} {
			if strings.Contains(tag, bad) {
				t.Errorf("field %s (json %q) looks like buyer contact data", typ.Field(i).Name, tag)
			}
		}
	}
}

func TestAHandoffWithoutWhatTheEmailNeedsIsNotDeliverable(t *testing.T) {
	ok := SupplierHandoffPayload{HandoffID: "h", SupplierEmail: "a@x.test", LabelObjectKey: "k",
		Lines: []SupplierHandoffLine{{SKU: "K", Quantity: 1}}}
	if !ok.Deliverable() {
		t.Fatal("a complete payload is not deliverable")
	}
	for name, mutate := range map[string]func(*SupplierHandoffPayload){
		"no handoff id":     func(p *SupplierHandoffPayload) { p.HandoffID = "" },
		"no supplier email": func(p *SupplierHandoffPayload) { p.SupplierEmail = "" },
		"no label":          func(p *SupplierHandoffPayload) { p.LabelObjectKey = "" },
		"no lines":          func(p *SupplierHandoffPayload) { p.Lines = nil },
	} {
		p := ok
		mutate(&p)
		if p.Deliverable() {
			t.Errorf("%s: deliverable, want not", name)
		}
	}
}
