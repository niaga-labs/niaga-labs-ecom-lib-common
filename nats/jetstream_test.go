package nats

import (
	"github.com/niaga-labs/niaga-labs-ecom-lib-common/eventsourcing"
	"strings"
	"testing"
)

func TestDefaultStreamsDoNotIncludeCatchAllEventsStream(t *testing.T) {
	for _, stream := range DefaultStreams {
		for _, subject := range stream.Subjects {
			if subject == "events.>" {
				t.Fatalf("stream %s uses catch-all events.>, which overlaps per-domain streams", stream.Name)
			}
		}
	}
}

// Phase 10 removed the legacy ORDERS/INVENTORY/PRODUCTS streams (and their
// bare order.>, inventory.>, product.> subject patterns). This test guards
// against re-introduction — the per-domain EVENTS_* streams are the single
// source of truth.
func TestDefaultStreamsDoNotIncludeLegacyStreams(t *testing.T) {
	bannedNames := map[string]struct{}{
		"ORDERS":        {},
		"INVENTORY":     {},
		"PRODUCTS":      {},
		"NOTIFICATIONS": {},
	}
	bannedSubjects := map[string]struct{}{
		"order.>":     {},
		"inventory.>": {},
		"product.>":   {},
	}

	for _, stream := range DefaultStreams {
		if _, banned := bannedNames[stream.Name]; banned {
			t.Errorf("legacy stream %s still in DefaultStreams", stream.Name)
		}
		for _, subject := range stream.Subjects {
			if _, banned := bannedSubjects[subject]; banned {
				t.Errorf("stream %s still binds legacy subject %s", stream.Name, subject)
			}
		}
	}
}

func TestDefaultStreamsIncludePerDomainEventStreams(t *testing.T) {
	want := map[string]string{
		"EVENTS_USER":        "events.user.>",
		"EVENTS_ORDER":       "events.order.>",
		"EVENTS_INVENTORY":   "events.inventory.>",
		"EVENTS_CATALOG":     "events.catalog.>",
		"EVENTS_SUPPORT":     "events.support.>",
		"EVENTS_MARKETPLACE": "events.marketplace.>",
		"EVENTS_CUSTOMER":    "events.customer.>",
	}

	for _, stream := range DefaultStreams {
		if subject, ok := want[stream.Name]; ok {
			if len(stream.Subjects) != 1 || stream.Subjects[0] != subject {
				t.Fatalf("%s subjects = %v, want [%s]", stream.Name, stream.Subjects, subject)
			}
			delete(want, stream.Name)
		}

		if strings.HasPrefix(stream.Name, "EVENTS_") && stream.MaxBytes <= 0 {
			t.Fatalf("%s MaxBytes must be set", stream.Name)
		}
	}

	for name := range want {
		t.Fatalf("missing default stream %s", name)
	}
}

// EVERY DOMAIN THE CATALOG DECLARES MUST HAVE A STREAM TO CARRY IT, and this
// test derives that from eventsourcing.SubjectDomains rather than a second
// hand-written list.
//
// The map above (TestDefaultStreamsIncludePerDomainEventStreams) is a fine
// regression guard for the entries it names, but it carries the flaw it exists
// to catch: a domain added next month and left out of THAT LIST is exactly as
// invisible as one left out of DefaultStreams. NIAGA-123 is what that gap looks
// like when it fires — events.customer.back_in_stock was declared and published
// for two commits with no stream to receive it.
//
// Raised in review of NIAGA-123 part 3a. No import cycle: eventsourcing imports
// the upstream nats.go, never this package.
func TestEverySubjectDomainHasAStream(t *testing.T) {
	// Domains that deliberately have no stream, with the reason. A domain here
	// is a decision, not an oversight — and when one stops being Reserved,
	// this test forces whoever un-reserves it to either add the stream or
	// justify the exception, rather than discovering it the way NIAGA-123 did.
	streamless := map[string]string{
		"agent": "SubjectAgentCommissionPaid is Reserved — declared, never published, never consumed " +
			"(NIAGA-117). Add EVENTS_AGENT before anything publishes it.",
	}

	haveStream := map[string]bool{}
	for _, s := range DefaultStreams {
		if strings.HasPrefix(s.Name, "EVENTS_") {
			haveStream[strings.ToLower(strings.TrimPrefix(s.Name, "EVENTS_"))] = true
		}
	}

	// A derivation that found nothing would pass while checking nothing.
	if len(haveStream) < 6 {
		t.Fatalf("only %d EVENTS_* streams found; the derivation is wrong, not the catalog", len(haveStream))
	}

	seen := map[string]bool{}
	for subject, domain := range eventsourcing.SubjectDomains {
		if seen[domain] {
			continue
		}
		seen[domain] = true

		if haveStream[domain] {
			continue
		}
		if why, ok := streamless[domain]; ok {
			t.Logf("domain %q has no stream, deliberately: %s", domain, why)
			continue
		}
		t.Errorf("domain %q (e.g. %s) has no EVENTS_%s stream: anything published on it fails, because "+
			"js.PublishMsg waits for a stream to acknowledge",
			domain, subject, strings.ToUpper(domain))
	}
}
