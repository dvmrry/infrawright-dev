package collectors

// Scheduler tests observe runFetchWorkers directly.
//
// The end-to-end test that used to cover this asserted on the order requests
// reached the transport. That order is not the scheduler's contract: work is
// handed out by take() under a mutex, and everything between take() returning
// and the HTTP call being issued is unsynchronised. A worker could take
// beta-a, be descheduled, and let another worker take and issue alpha-b first
// -- perfect rotation, wrong-looking transport log. The test failed on loaded
// CI runners for that reason while the property it names still held.
//
// Handing out work is what runFetchWorkers decides, so that is what these
// assert, with no transport, no delays, and no timing assumptions.

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func schedulerWorkItems(t *testing.T, plan ...string) []fetchWorkItem {
	t.Helper()
	items := make([]fetchWorkItem, 0, len(plan))
	for index, spec := range plan {
		product, suffix, found := splitSchedulerSpec(spec)
		if !found {
			t.Fatalf("work item spec %q must be <product>/<suffix>", spec)
		}
		items = append(items, fetchWorkItem{
			entry:        FetchEntry{Product: product, Path: product + "-" + suffix},
			index:        index,
			resourceType: product + "_" + suffix,
		})
	}
	return items
}

func splitSchedulerSpec(spec string) (product, suffix string, found bool) {
	for i := 0; i < len(spec); i++ {
		if spec[i] == '/' {
			return spec[:i], spec[i+1:], true
		}
	}
	return "", "", false
}

// recordFirstRound runs the scheduler and returns the resource types handed
// out to the first full set of workers, plus every type handed out overall.
//
// Only the first round is observable in order. execute() runs in the worker
// goroutine immediately after take() returns, with no synchronisation between
// the two, so two workers can take in one order and reach execute in the
// other. Holding every worker until the batch is full closes that gap for the
// first round and for no round after it -- which is why the rotation
// assertions below stop there rather than pinning a whole sequence. Asserting
// past what the mechanism serialises is the defect this file replaced.
func recordFirstRound(concurrency int, items []fetchWorkItem) (firstRound, all []string) {
	var mu sync.Mutex
	all = make([]string, 0, len(items))

	barrier := make(chan struct{})
	var once sync.Once
	arrived := 0

	runFetchWorkers(concurrency, items, func(item fetchWorkItem) fetchOutcome {
		mu.Lock()
		all = append(all, item.resourceType)
		if len(firstRound) < concurrency {
			firstRound = append(firstRound, item.resourceType)
		}
		arrived++
		full := arrived >= concurrency
		mu.Unlock()
		if full {
			once.Do(func() { close(barrier) })
		}
		<-barrier
		return fetchOutcome{kind: outcomeProcessed, resourceType: item.resourceType}
	})
	return firstRound, all
}

func productsOf(resourceTypes []string) map[string]struct{} {
	products := map[string]struct{}{}
	for _, resourceType := range resourceTypes {
		products[strings.SplitN(resourceType, "_", 2)[0]] = struct{}{}
	}
	return products
}

// TestFetchWorkersRotateProductsInsteadOfDrainingOne pins the scheduling
// contract: the first items handed out come from distinct products, so one
// large product queue cannot consume every worker while another waits.
func TestFetchWorkersRotateProductsInsteadOfDrainingOne(t *testing.T) {
	items := schedulerWorkItems(t, "alpha/a", "alpha/b", "beta/a", "beta/b")

	firstRound, _ := recordFirstRound(2, items)
	if got := productsOf(firstRound); len(got) != 2 {
		t.Errorf(
			"runFetchWorkers(concurrency 2) handed out %v to the first two workers, "+
				"drawn from %d product(s); want one from each",
			firstRound, len(got),
		)
	}
}

// Three products, three workers, uneven queue lengths: the first round must
// still take one from each rather than twice from the longest.
func TestFetchWorkersRotateAcrossUnevenQueues(t *testing.T) {
	items := schedulerWorkItems(t,
		"alpha/a", "alpha/b", "alpha/c", "beta/a", "gamma/a", "gamma/b",
	)

	firstRound, all := recordFirstRound(3, items)
	if got := productsOf(firstRound); len(got) != 3 {
		t.Errorf(
			"runFetchWorkers(uneven queues) handed out %v to the first three workers, "+
				"drawn from %d product(s); want one from each",
			firstRound, len(got),
		)
	}
	if len(all) != len(items) {
		t.Errorf("handed out %d items, want all %d", len(all), len(items))
	}
}

// Serial mode is a separate branch in runFetchWorkers and keeps registry
// order, which is the sequential contract the report relies on. With one
// worker there is no take/execute gap, so the whole sequence is observable.
func TestFetchWorkersKeepRegistryOrderWhenSerial(t *testing.T) {
	items := schedulerWorkItems(t, "alpha/a", "alpha/b", "beta/a", "beta/b")

	_, all := recordFirstRound(1, items)
	want := []string{"alpha_a", "alpha_b", "beta_a", "beta_b"}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("runFetchWorkers(concurrency 1) handed out %v, want registry order %v", all, want)
	}
}

// Every item must be handed out exactly once whatever the rotation does.
func TestFetchWorkersHandOutEveryItemExactlyOnce(t *testing.T) {
	specs := make([]string, 0, 12)
	for _, product := range []string{"alpha", "beta", "gamma"} {
		for suffix := 0; suffix < 4; suffix++ {
			specs = append(specs, fmt.Sprintf("%s/s%d", product, suffix))
		}
	}
	items := schedulerWorkItems(t, specs...)

	_, all := recordFirstRound(3, items)
	seen := map[string]int{}
	for _, resourceType := range all {
		seen[resourceType]++
	}
	if len(seen) != len(items) {
		t.Errorf("handed out %d distinct items, want %d", len(seen), len(items))
	}
	for resourceType, count := range seen {
		if count != 1 {
			t.Errorf("%s handed out %d times, want once", resourceType, count)
		}
	}
}
