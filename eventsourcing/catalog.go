package eventsourcing

// Canonical NATS event subjects. Phase 10 removes the legacy subjects from
// service code after every publisher and durable consumer has migrated here.
//
// EVERY SUBJECT HERE HAS A PUBLISHER AND A CONSUMER, OR SAYS WHY NOT. The full
// subject > publisher > consumers table is in README.md and was measured rather
// than assumed (NIAGA-117). Two subjects are Reserved and two are published with
// no consumer; both cases are commented where they are declared.
const (
	SubjectUserRegistered             = "events.user.registered"
	SubjectUserPasswordResetRequested = "events.user.password_reset_requested"

	SubjectOrderCreated       = "events.order.created"
	SubjectOrderConfirmed     = "events.order.confirmed"
	SubjectOrderCancelled     = "events.order.cancelled"
	SubjectOrderStatusChanged = "events.order.status_changed"

	SubjectPaymentReceived = "events.order.payment.received"
	SubjectPaymentVerified = "events.order.payment.verified"
	SubjectPaymentRejected = "events.order.payment.rejected"
	SubjectPaymentRefunded = "events.order.payment.refunded"

	SubjectInventoryStockUpdated     = "events.inventory.stock.updated"
	SubjectInventoryProductRestocked = "events.inventory.product.restocked"

	SubjectCatalogProductCreated       = "events.catalog.product.created"
	SubjectCatalogProductUpdated       = "events.catalog.product.updated"
	SubjectCatalogProductDeleted       = "events.catalog.product.deleted"
	SubjectCatalogFlashSaleActivated   = "events.catalog.flash_sale.activated"
	SubjectCatalogFlashSaleDeactivated = "events.catalog.flash_sale.deactivated"

	SubjectSupportTicketCreated  = "events.support.ticket.created"
	SubjectSupportTicketReplied  = "events.support.ticket.replied"
	SubjectSupportTicketResolved = "events.support.ticket.resolved"

	// PUBLISHED BY service-marketplace, CONSUMED BY NOBODY (audited 2026-09-06,
	// NIAGA-117). Not a defect and not reserved — these are real events on the
	// wire that no service subscribes to yet. Recorded here so the next reader
	// does not have to grep for a consumer that does not exist.
	SubjectMarketplaceSyncCompleted = "events.marketplace.sync.completed"
	SubjectMarketplaceSyncFailed    = "events.marketplace.sync.failed"

	// IN PROGRESS — NIAGA-277 (dropship hand-off). Declared ahead of both ends,
	// the same way SubjectCustomerBackInStock was: service-marketplace publishes
	// it when an admin sends a marketplace order to its supplier (NIAGA-548), and
	// service-notification consumes it to email that supplier the shipping label
	// and the item lines (NIAGA-549). One event per supplier per hand-off. The
	// payload is SupplierHandoffPayload (supplier_handoff.go). Carried by
	// EVENTS_MARKETPLACE (events.marketplace.>).
	SubjectMarketplaceSupplierHandoff = "events.marketplace.order.supplier_handoff"

	// IN PROGRESS — NIAGA-123. Not Reserved: this one is mid-build, and the
	// publisher and consumer land in the same ticket. Deliberately declared ahead
	// of both ends so service-customer and service-notification have one shape to
	// build against rather than two guesses.
	//
	// It is DOWNSTREAM of SubjectInventoryProductRestocked, not a duplicate of it:
	// inventory says a product came back; this says a NAMED CUSTOMER ASKED TO BE
	// TOLD. The subscription lookup between the two is service-customer's job, so
	// notification never has to know what a subscription is.
	//
	// If this is still the only reference to it long after NIAGA-123 closed, it
	// has become Reserved by accident — check both ends before assuming it works.
	SubjectCustomerBackInStock = "events.customer.back_in_stock"

	// RESERVED — DECLARED, NEVER PUBLISHED, NEVER CONSUMED (NIAGA-117).
	//
	// Audited 2026-09-06 across every repo in the workspace, by two independent
	// methods: a search for `eventsourcing.<Const>` in all Go files, and a
	// literal search for the subject STRING in every tracked file of every repo,
	// frontends included. Both agree — these two appear NOWHERE but this file.
	//
	// Kept rather than deleted, deliberately. Nothing imports them, so removing
	// them would be safe today; but they name work that is planned rather than
	// abandoned (service-customer exists, service-agent is legacy-hidden rather
	// than deleted), and the name is the only surviving record of the intent.
	// Deleting costs that and saves nothing. Re-check before publishing either:
	// a subject with no consumer is a message into a void, and the marketplace
	// pair below is already in that state.
	SubjectCustomerCreated     = "events.customer.created"
	SubjectAgentCommissionPaid = "events.agent.commission.paid"
)

var SubjectDomains = map[string]string{
	SubjectUserRegistered:              "user",
	SubjectUserPasswordResetRequested:  "user",
	SubjectOrderCreated:                "order",
	SubjectOrderConfirmed:              "order",
	SubjectOrderCancelled:              "order",
	SubjectOrderStatusChanged:          "order",
	SubjectPaymentReceived:             "order",
	SubjectPaymentVerified:             "order",
	SubjectPaymentRejected:             "order",
	SubjectPaymentRefunded:             "order",
	SubjectInventoryStockUpdated:       "inventory",
	SubjectInventoryProductRestocked:   "inventory",
	SubjectCatalogProductCreated:       "catalog",
	SubjectCatalogProductUpdated:       "catalog",
	SubjectCatalogProductDeleted:       "catalog",
	SubjectCatalogFlashSaleActivated:   "catalog",
	SubjectCatalogFlashSaleDeactivated: "catalog",
	SubjectSupportTicketCreated:        "support",
	SubjectSupportTicketReplied:        "support",
	SubjectSupportTicketResolved:       "support",
	SubjectMarketplaceSyncCompleted:    "marketplace",
	SubjectMarketplaceSyncFailed:       "marketplace",
	SubjectMarketplaceSupplierHandoff:  "marketplace",
	SubjectCustomerBackInStock:         "customer",
	SubjectCustomerCreated:             "customer",
	SubjectAgentCommissionPaid:         "agent",
}

type UserRegisteredPayload struct {
	UserID string `json:"user_id"`
	Email  string `json:"email,omitempty"`
	Name   string `json:"name,omitempty"`
}

type OrderEventPayload struct {
	OrderID    string `json:"order_id"`
	CustomerID string `json:"customer_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

type PaymentEventPayload struct {
	PaymentID string `json:"payment_id,omitempty"`
	OrderID   string `json:"order_id"`
	Provider  string `json:"provider,omitempty"`
	Amount    string `json:"amount,omitempty"`
}

type InventoryStockUpdatedPayload struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku,omitempty"`
	Quantity  int    `json:"quantity"`
}

type CatalogProductPayload struct {
	ProductID   string `json:"product_id"`
	ProductType string `json:"product_type,omitempty"`
	Name        string `json:"name,omitempty"`
}

type SupportTicketPayload struct {
	TicketID   string `json:"ticket_id"`
	CustomerID string `json:"customer_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

// CustomerBackInStockPayload carries everything the email needs, so
// service-notification never queries a customer or a product.
//
// A notification consumer that has to look things up is a consumer that fails
// when the other service is down, and an email nobody can explain afterwards.
// The publisher already has all of this in hand at the moment it matches the
// subscription.
type CustomerBackInStockPayload struct {
	SubscriptionID string `json:"subscription_id"`
	CustomerID     string `json:"customer_id"`
	CustomerEmail  string `json:"customer_email"`
	CustomerName   string `json:"customer_name,omitempty"`
	ProductID      string `json:"product_id"`
	ProductName    string `json:"product_name"`
	ProductSlug    string `json:"product_slug,omitempty"`
	ProductImage   string `json:"product_image,omitempty"`

	// ProductURL is the FULL link the email's call-to-action uses, built by the
	// publisher — the same convention every other templated email here follows
	// (reset_url, verification_url, cart_url in service-notification's types).
	//
	// The publisher builds it so the CONSUMER NEVER HAS TO KNOW THE STOREFRONT'S
	// ROUTE SHAPE. Handing over only a slug would make service-notification own
	// the base URL and the /products/:slug pattern, and every route change would
	// then have to be made in a service that has nothing to do with the
	// storefront. product_slug stays for templates that want the bare value.
	ProductURL string `json:"product_url,omitempty"`

	// The variant fields are omitempty because a product without variants has
	// none, and an empty string in the template renders as a blank line rather
	// than being skipped.
	VariantID   string `json:"variant_id,omitempty"`
	VariantSKU  string `json:"variant_sku,omitempty"`
	VariantName string `json:"variant_name,omitempty"`

	StockQuantity int `json:"stock_quantity"`
}

type MarketplaceSyncPayload struct {
	Marketplace string `json:"marketplace"`
	SyncID      string `json:"sync_id,omitempty"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}
