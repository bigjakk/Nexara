package handlers

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var seriesT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func seriesAt(s int) time.Time { return seriesT0.Add(time.Duration(s) * time.Second) }

// readSeries is one entity's samples, 10 s apart, its disk_read counter
// carrying reads; the entity shows CPU, so it is running.
func readSeries(reads ...float64) []rawSample {
	out := make([]rawSample, len(reads))
	for i, v := range reads {
		out[i] = rawSample{at: seriesAt(10 * i), cpu: 0.5, memUsed: 1, memTotal: 4, diskRead: v}
	}
	return out
}

func readRates(samples []ratedSample) []any {
	out := make([]any, len(samples))
	for i, s := range samples {
		if s.diskReadBps != nil {
			out[i] = *s.diskReadBps
		}
	}
	return out
}

// TestRateSamples pins the counter rules that let a chart at sample
// resolution show a stall as a stall: no zero dip for an unreported or fallen
// counter, no spike when a dipped sum recovers, and a reset that re-bases.
func TestRateSamples(t *testing.T) {
	t.Parallel()
	stopped := readSeries(100, 0, 0)
	for i := range stopped {
		stopped[i].cpu, stopped[i].memUsed = 0, 0
	}
	// The counter falls to 10 and climbs slowly for longer than counterReach,
	// then jumps past where it fell from: measured from the reading before,
	// not from the forgotten high.
	forgotten := readSeries(1000)
	for s := 10; s <= 620; s += 10 {
		forgotten = append(forgotten, rawSample{at: seriesAt(s), diskRead: float64(s), cpu: 0.5})
	}
	forgotten = append(forgotten, rawSample{at: seriesAt(630), diskRead: 1630, cpu: 0.5})

	for _, tt := range []struct {
		name    string
		samples []rawSample
		guest   bool
		want    []any
	}{
		{"steady", readSeries(0, 100, 300), false, []any{nil, 10.0, 20.0}},
		{"an idle stretch is measured from its newest reading", readSeries(0, 100, 100, 200), false,
			[]any{nil, 10.0, 0.0, 10.0}},
		{"unreported is measured across", func() []rawSample {
			s := readSeries(0, 0, 400)
			s[1].diskUnreported = true
			return s
		}(), false, []any{nil, nil, 20.0}},
		{"a running guest's zero is no reading", readSeries(100, 0, 50), true, []any{nil, nil, nil}},
		{"a stopped guest's zero is a reading", stopped, true, []any{nil, nil, 0.0}},
		{"a node's zero is a fall", readSeries(100, 0, 50), false, []any{nil, nil, 5.0}},
		{"a reset re-bases", readSeries(1000, 10, 110), false, []any{nil, nil, 10.0}},
		{"a sum that dips in steps and recovers is measured from its high",
			readSeries(1000, 500, 200, 1600), false, []any{nil, nil, nil, 20.0}},
	} {
		got := readRates(rateSamples(tt.samples, tt.guest))
		if len(got) != len(tt.want) {
			t.Fatalf("%s: %d rates, want %d", tt.name, len(got), len(tt.want))
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: rate %d = %v, want %v", tt.name, i, got[i], tt.want[i])
			}
		}
	}

	rated := rateSamples(forgotten, false)
	if got := readRates(rated[len(rated)-1:])[0]; got != 101.0 {
		t.Errorf("after counterReach: rate = %v, want 101 — the high it fell from must be forgotten", got)
	}

	// A running node's 0% CPU is a fresh pveproxy worker's, not a reading; an
	// offline node's (no memory either) and a stopped guest's are readings.
	zero := readSeries(0, 10, 20)
	zero[1].cpu = 0
	if got := rateSamples(zero, false); got[1].cpu != nil || *got[0].cpu != 0.5 || *got[2].cpu != 0.5 {
		t.Errorf("a running node's zero CPU = %v, want none, with its neighbours' readings kept", got[1].cpu)
	}
	zero[1].memUsed = 0
	if node, guest := rateSamples(zero, false)[1].cpu, rateSamples(zero, true)[1].cpu; node == nil || guest == nil {
		t.Errorf("zero CPU without memory: node %v, guest %v, want both kept as readings", node, guest)
	}
}

// TestSeriesShaping pins what reaches the chart: the window starts at since,
// buckets average what they hold and sit at their samples' time, a cluster is
// the total of its nodes, rollups rate bucket to bucket, and a stretch with no
// samples is a break, not a ramp.
func TestSeriesShaping(t *testing.T) {
	t.Parallel()
	f := func(v float64) *float64 { return &v }
	sample := func(s int, cpu float64, rate *float64) ratedSample {
		return ratedSample{at: seriesAt(s), cpu: f(cpu), memUsed: 1, memTotal: 4,
			diskReadBps: rate, diskWriteBps: rate, netInBps: rate, netOutBps: rate}
	}

	t.Run("buckets", func(t *testing.T) {
		t.Parallel()
		samples := []ratedSample{
			sample(-10, 1, f(999)), // before since
			sample(0, 0.2, f(10)), {at: seriesAt(30), memUsed: 1, memTotal: 4}, sample(60, 0.6, f(30)),
			sample(130, 0.1, f(5)),
		}
		got := bucketSamples(samples, seriesAt(0), 2*time.Minute)
		if len(got) != 2 {
			t.Fatalf("%d buckets, want 2", len(got))
		}
		if *got[0].CPUPercent != 40 || *got[0].DiskReadBps != 20 || *got[0].MemPercent != 25 {
			t.Errorf("first bucket = cpu %v disk %v mem %v, want 40, 20 (values not read skipped), 25",
				*got[0].CPUPercent, *got[0].DiskReadBps, *got[0].MemPercent)
		}
		if got[0].Timestamp != seriesAt(30).UnixMilli() || got[1].Timestamp != seriesAt(130).UnixMilli() {
			t.Errorf("bucket times = %d, %d, want its samples' mean time and a lone sample's own",
				got[0].Timestamp, got[1].Timestamp)
		}
	})

	t.Run("cluster total", func(t *testing.T) {
		t.Parallel()
		a, b, c := uuid.New(), uuid.New(), uuid.New()
		merged := mergeNodes(map[uuid.UUID][]ratedSample{
			a: {sample(0, 0.25, f(10)), sample(10, 0.25, f(10)), sample(20, 0.25, f(10))},
			b: {{at: seriesAt(0), cpu: f(0.75), memUsed: 3, memTotal: 4, diskReadBps: f(30), diskWriteBps: f(30), netInBps: f(30), netOutBps: f(30)},
				{at: seriesAt(10), cpu: f(0.75), memUsed: 3, memTotal: 4, diskReadBps: f(30), diskWriteBps: f(1), netInBps: f(1), netOutBps: f(1)},
				{at: seriesAt(20), cpu: nil, memUsed: 3, memTotal: 4, diskReadBps: nil, diskWriteBps: f(1), netInBps: f(1), netOutBps: f(1)}},
			// c missed the 10 s cycle.
			c: {sample(0, 0.5, f(5)), sample(20, 0.5, f(5))},
		})
		if len(merged) != 3 || *merged[0].cpu != 0.5 || merged[0].memUsed != 5 || merged[0].memTotal != 12 {
			t.Fatalf("merged[0] = %+v, want mean CPU 0.5 and summed memory 5 of 12", merged[0])
		}
		if *merged[2].cpu != 0.375 {
			t.Errorf("merged CPU at 20 s = %v, want the mean of the nodes that read it", *merged[2].cpu)
		}
		if *merged[0].diskReadBps != 45 {
			t.Errorf("merged disk read = %v, want the nodes' total, 45", *merged[0].diskReadBps)
		}
		if merged[1].netInBps != nil {
			t.Errorf("merged net in at 10 s = %v, want none: a node missed that cycle", *merged[1].netInBps)
		}
		if merged[2].diskReadBps != nil || *merged[2].netInBps != 16 {
			t.Errorf("merged at 20 s = disk %v, net %v, want no disk total (a node has no rate there) and net 16",
				merged[2].diskReadBps, *merged[2].netInBps)
		}
	})

	t.Run("rollups", func(t *testing.T) {
		t.Parallel()
		rows := []rollupRow{
			{bucket: seriesAt(0), diskRead: 0},
			{bucket: seriesAt(300), diskRead: 3000},
			{bucket: seriesAt(600), diskRead: 1000},
		}
		if got := readRates(rollupSamples(rows)); got[0] != nil || got[1] != 10.0 || got[2] != nil {
			t.Errorf("rollup rates = %v, want none for the first bucket and the fall", got)
		}
	})

	t.Run("gaps", func(t *testing.T) {
		t.Parallel()
		points := func(seconds ...int) []metricPoint {
			out := make([]metricPoint, 0, len(seconds))
			for _, s := range seconds {
				out = append(out, metricPoint{Timestamp: seriesAt(s).UnixMilli(), CPUPercent: f(1)})
			}
			return out
		}
		got := withGaps(points(0, 10, 20, 100, 110))
		if len(got) != 6 || got[3].CPUPercent != nil || got[3].Timestamp != seriesAt(60).UnixMilli() {
			t.Fatalf("withGaps = %+v, want one empty point at 60 s between 20 s and 100 s", got)
		}
		// Samples 30 s apart in 10 s buckets: their own spacing, not a gap.
		if got := withGaps(points(0, 30, 60, 90, 120)); len(got) != 5 {
			t.Errorf("withGaps on an even 30 s series added %d points", len(got)-5)
		}
	})
}
