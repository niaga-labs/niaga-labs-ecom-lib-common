package eventsourcing

import "time"

// SupplierHandoffPayload is the contract for SubjectMarketplaceSupplierHandoff
// (NIAGA-277): "send this marketplace order's shipping label and item lines to
// this supplier". service-marketplace publishes it; service-notification
// decodes into it directly and emails the supplier. Neither side may keep its
// own copy — order_payloads.go records what five silent drifts cost when they
// did.
//
// It carries everything the email needs, so the consumer never queries the
// order, the product or the supplier (the CustomerBackInStockPayload rule).
//
// What it deliberately does NOT carry:
//   - the label PDF itself. It travels by reference (LabelObjectKey, a MinIO
//     key the publisher stored), because a message bus is no place for binary
//     payloads and JetStream keeps every message for the stream's retention.
//   - any buyer contact beyond what is printed on the label. The supplier gets
//     the label and the lines; the buyer's phone and email stay with us.
type SupplierHandoffPayload struct {
	// HandoffID identifies this send. A redelivery carries the same one, so the
	// consumer can refuse to email twice.
	HandoffID string `json:"handoff_id"`

	// The marketplace order, as the platform names it and as we store it.
	MarketplaceOrderID string     `json:"marketplace_order_id"`
	Platform           string     `json:"platform"`          // "shopee", "tiktok", "lazada"
	PlatformOrderSN    string     `json:"platform_order_sn"` // the platform's own order number
	ShipBy             *time.Time `json:"ship_by,omitempty"` // the platform's ship-by deadline, when it gives one

	SupplierID    string `json:"supplier_id"`
	SupplierName  string `json:"supplier_name"`
	SupplierEmail string `json:"supplier_email"`

	// LabelObjectKey is the MinIO object key of the shipping label PDF.
	LabelObjectKey string `json:"label_object_key"`
	// LabelFilename is what the attachment is called in the email.
	LabelFilename string `json:"label_filename"`

	// Lines are only this supplier's lines of the order.
	Lines []SupplierHandoffLine `json:"lines"`
}

// SupplierHandoffLine is one item the supplier packs.
type SupplierHandoffLine struct {
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	VariantName string `json:"variant_name,omitempty"`
	Quantity    int    `json:"quantity"`
}

// Deliverable reports whether the consumer can act on the payload: it needs a
// supplier to email, a label to attach and at least one line to list. A
// payload that fails this is a publisher bug, not something to retry.
func (p SupplierHandoffPayload) Deliverable() bool {
	return p.HandoffID != "" && p.SupplierEmail != "" && p.LabelObjectKey != "" && len(p.Lines) > 0
}
