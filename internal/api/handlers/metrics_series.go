package handlers

import (
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
)

// counterReach is how long a counter's highest reading is remembered: a
// reading that climbs back to it within this long is measured from it, so a
// node sum that dips while a guest goes unreported in an unmarked sample, and
// then recovers, does not count the guest's lifetime of bytes as one sample's.
// It is the alert engine's counterReach (internal/notifications/alert_engine.go).
const counterReach = 10 * time.Minute

// seriesPoints is how many points a raw-sample range is bucketed down to.
const seriesPoints = 360

// rawSample is one stored sample of a node's or guest's metrics, oldest first.
type rawSample struct {
	at                                 time.Time
	cpu, memUsed, memTotal             float64
	diskRead, diskWrite, netIn, netOut float64
	diskUnreported                     bool
}

// rawFetchSince is where a raw-sample range's fetch starts: counterReach
// before the window, so the window's first readings are measured from the
// ones before them, and from a high a dip within reach fell from.
func rawFetchSince(since time.Time) time.Time { return since.Add(-counterReach) }

// oldestFirst turns a newest-first fetch — the alert engine's queries read
// samples that way — into samples oldest first, as rateSamples rates them.
func oldestFirst[R any](rows []R, sample func(R) rawSample) []rawSample {
	out := make([]rawSample, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = sample(r)
	}
	return out
}

// ratedSample is a sample with its counters turned into rates. A nil rate is
// no measurement: the counter fell, was unreported, or has no reading before
// it. A nil cpu is no reading either (see rateSamples).
type ratedSample struct {
	at                                             time.Time
	cpu                                            *float64
	memUsed, memTotal                              float64
	diskReadBps, diskWriteBps, netInBps, netOutBps *float64
}

type counterReading struct {
	v  float64
	at time.Time
}

// counterTrack derives one cumulative counter's rates, reading by reading:
//
//   - a reading below the one before it has no rate: the counter fell — a
//     guest restarted, stopped or left the node, or a node's sum missed one;
//   - a reading at or above the highest of the last counterReach is measured
//     from that highest one (the newest reading at that level), so a sum that
//     dipped and recovered does not count the returning guest's lifetime of
//     bytes as one sample's;
//   - any other reading is measured from the one before it.
//
// Unlike the alert engine's counterRates, which has no rate below its peak,
// this re-bases on the reading after a fall, so a reset costs one sample of
// chart rather than up to counterReach of it. The price is one inflated rate
// when a sum loses one guest while another returns, and one understated rate
// when a counter reset climbs back past its old high within counterReach.
type counterTrack struct {
	recent []counterReading // reported readings within counterReach, oldest first
}

func (c *counterTrack) next(v float64, at time.Time, reported bool) *float64 {
	if !reported {
		return nil
	}
	cut := at.Add(-counterReach)
	drop := 0
	for drop < len(c.recent) && c.recent[drop].at.Before(cut) {
		drop++
	}
	c.recent = c.recent[drop:]

	var rate *float64
	if n := len(c.recent); n > 0 {
		prev := c.recent[n-1]
		if v >= prev.v {
			base := prev
			if high := c.highest(); v >= high.v {
				base = high
			}
			if dt := at.Sub(base.at).Seconds(); dt > 0 {
				r := (v - base.v) / dt
				rate = &r
			}
		}
	}
	c.recent = append(c.recent, counterReading{v: v, at: at})
	return rate
}

// highest is the highest recent reading, the newest one at that level.
func (c *counterTrack) highest() counterReading {
	high := c.recent[0]
	for _, r := range c.recent[1:] {
		if r.v >= high.v {
			high = r
		}
	}
	return high
}

// rateSamples turns one entity's samples, oldest first, into rated samples.
// Disk counters flagged disk_unreported are no readings: PVE left them
// unreported and the collector carried the last ones forward.
//
// For a guest, neither is a zero counter on a sample showing CPU or memory —
// PVE's unreported reading before that flag existed, or after the collector
// restarts and has no carry to apply. A stopped guest's zeros are readings.
//
// For a running node, a CPU reading of exactly 0 is none: a pveproxy worker
// answers its first node status call with 0, having no earlier /proc/stat
// reading to measure from (PVE::ProcFSTools::read_proc_stat), and workers are
// recycled often enough that a node reads 0 several times an hour. A running
// node never measures exactly 0, and charted, that reading is a stall that
// never was. An offline node's 0 — no memory either — is its reading.
func rateSamples(samples []rawSample, guest bool) []ratedSample {
	var dr, dw, ni, no counterTrack
	out := make([]ratedSample, len(samples))
	for i, s := range samples {
		live := s.cpu != 0 || s.memUsed != 0
		reported := func(v float64, flagged bool) bool {
			return !flagged && (!guest || v != 0 || !live)
		}
		var cpu *float64
		if guest || s.cpu != 0 || s.memUsed == 0 {
			v := s.cpu
			cpu = &v
		}
		out[i] = ratedSample{
			at:           s.at,
			cpu:          cpu,
			memUsed:      s.memUsed,
			memTotal:     s.memTotal,
			diskReadBps:  dr.next(s.diskRead, s.at, reported(s.diskRead, s.diskUnreported)),
			diskWriteBps: dw.next(s.diskWrite, s.at, reported(s.diskWrite, s.diskUnreported)),
			netInBps:     ni.next(s.netIn, s.at, reported(s.netIn, false)),
			netOutBps:    no.next(s.netOut, s.at, reported(s.netOut, false)),
		}
	}
	return out
}

// rollupRow is one bucket of a continuous aggregate: the averages of the
// gauges and of the cumulative counters over the bucket.
type rollupRow struct {
	bucket                                                     time.Time
	cpu, memUsed, memTotal, diskRead, diskWrite, netIn, netOut float64
}

// rollupSamples turns a rollup's buckets, oldest first, into rated samples,
// each rate measured from the bucket before: none for the first bucket, or
// where the counter fell (a reboot), rather than a negative spike or a false
// zero.
func rollupSamples(rows []rollupRow) []ratedSample {
	out := make([]ratedSample, len(rows))
	var prev *rollupRow
	for i := range rows {
		r := &rows[i]
		s := ratedSample{at: r.bucket, cpu: &r.cpu, memUsed: r.memUsed, memTotal: r.memTotal}
		if prev != nil {
			elapsed := r.bucket.Sub(prev.bucket).Seconds()
			s.diskReadBps = ioRate(r.diskRead, prev.diskRead, elapsed)
			s.diskWriteBps = ioRate(r.diskWrite, prev.diskWrite, elapsed)
			s.netInBps = ioRate(r.netIn, prev.netIn, elapsed)
			s.netOutBps = ioRate(r.netOut, prev.netOut, elapsed)
		}
		out[i] = s
		prev = r
	}
	return out
}

func ioRate(cur, prev, elapsed float64) *float64 {
	if elapsed <= 0 || cur < prev {
		return nil
	}
	r := (cur - prev) / elapsed
	return &r
}

// mergeNodes combines a cluster's per-node rated samples into one series per
// collection cycle — every node of a cycle shares one timestamp — with CPU
// averaged over the nodes that read it and memory as the sum of used over the
// sum of total across the nodes present, and each rate the cluster's total: the sum of the nodes'
// rates, as the live view shows it. A total is left out at a timestamp where a
// node whose series spans it has no rate there, or no sample at all, so one
// node's unreported sample or missed cycle cannot dip the line.
func mergeNodes(perNode map[uuid.UUID][]ratedSample) []ratedSample {
	type span struct{ first, last int64 }
	type acc struct {
		at                     time.Time
		n, cpuN                int
		cpu, memUsed, memTotal float64
		rates                  [4]float64
		missing                [4]bool
	}
	spans := make([]span, 0, len(perNode))
	byTime := map[int64]*acc{}
	for _, samples := range perNode {
		if len(samples) == 0 {
			continue
		}
		spans = append(spans, span{samples[0].at.UnixMicro(), samples[len(samples)-1].at.UnixMicro()})
		for _, s := range samples {
			key := s.at.UnixMicro()
			a := byTime[key]
			if a == nil {
				a = &acc{at: s.at}
				byTime[key] = a
			}
			a.n++
			if s.cpu != nil {
				a.cpu += *s.cpu
				a.cpuN++
			}
			a.memUsed += s.memUsed
			a.memTotal += s.memTotal
			for i, r := range [4]*float64{s.diskReadBps, s.diskWriteBps, s.netInBps, s.netOutBps} {
				if r == nil {
					a.missing[i] = true
				} else {
					a.rates[i] += *r
				}
			}
		}
	}
	out := make([]ratedSample, 0, len(byTime))
	for key, a := range byTime {
		spanned := 0
		for _, sp := range spans {
			if sp.first <= key && key <= sp.last {
				spanned++
			}
		}
		total := func(i int) *float64 {
			if a.missing[i] || a.n < spanned {
				return nil
			}
			v := a.rates[i]
			return &v
		}
		var cpu *float64
		if a.cpuN > 0 {
			mean := a.cpu / float64(a.cpuN)
			cpu = &mean
		}
		out = append(out, ratedSample{
			at:           a.at,
			cpu:          cpu,
			memUsed:      a.memUsed,
			memTotal:     a.memTotal,
			diskReadBps:  total(0),
			diskWriteBps: total(1),
			netInBps:     total(2),
			netOutBps:    total(3),
		})
	}
	slices.SortFunc(out, func(a, b ratedSample) int { return a.at.Compare(b.at) })
	return out
}

// bucketSamples averages the rated samples from since on into buckets of
// width, each value over the samples that have one. A point sits at the mean
// time of the samples it averages — a bucket of one sample at that sample's
// own time, which a live tick for the same sample carries too. A bucket no
// sample falls in is left out; withGaps marks it.
func bucketSamples(samples []ratedSample, since time.Time, width time.Duration) []metricPoint {
	type acc struct {
		start  time.Time
		n      int
		atSum  int64
		cpu    float64
		cpuN   int
		memPct float64
		memN   int
		sums   [4]float64
		counts [4]int
	}
	var buckets []*acc
	for _, s := range samples {
		if s.at.Before(since) {
			continue
		}
		start := s.at.Truncate(width)
		if len(buckets) == 0 || !buckets[len(buckets)-1].start.Equal(start) {
			buckets = append(buckets, &acc{start: start})
		}
		b := buckets[len(buckets)-1]
		b.n++
		b.atSum += s.at.UnixMilli()
		if s.cpu != nil {
			b.cpu += *s.cpu
			b.cpuN++
		}
		if s.memTotal > 0 {
			b.memPct += s.memUsed / s.memTotal * 100
			b.memN++
		}
		for i, r := range [4]*float64{s.diskReadBps, s.diskWriteBps, s.netInBps, s.netOutBps} {
			if r != nil {
				b.sums[i] += *r
				b.counts[i]++
			}
		}
	}
	points := make([]metricPoint, len(buckets))
	for i, b := range buckets {
		avg := func(j int) *float64 {
			if b.counts[j] == 0 {
				return nil
			}
			return roundTo(b.sums[j]/float64(b.counts[j]), 0)
		}
		p := metricPoint{
			Timestamp:    b.atSum / int64(b.n),
			DiskReadBps:  avg(0),
			DiskWriteBps: avg(1),
			NetInBps:     avg(2),
			NetOutBps:    avg(3),
		}
		if b.cpuN > 0 {
			p.CPUPercent = roundTo(b.cpu/float64(b.cpuN)*100, 2)
		}
		if b.memN > 0 {
			p.MemPercent = roundTo(b.memPct/float64(b.memN), 2)
		}
		points[i] = p
	}
	return points
}

// withGaps inserts a point with no values wherever two consecutive points are
// further apart than 2.5 times the series' median spacing, so a chart breaks
// its line where nothing was sampled instead of drawing a ramp across the gap.
// at reads a point's time; empty makes a point with no values at a time.
func withGaps[P any](points []P, at func(P) int64, empty func(int64) P) []P {
	if len(points) < 3 {
		return points
	}
	spacing := make([]int64, len(points)-1)
	for i := 1; i < len(points); i++ {
		spacing[i-1] = at(points[i]) - at(points[i-1])
	}
	slices.Sort(spacing)
	limit := spacing[len(spacing)/2] * 5 / 2
	out := make([]P, 0, len(points))
	for i, p := range points {
		if i > 0 && at(p)-at(points[i-1]) > limit {
			out = append(out, empty((at(points[i-1])+at(p))/2))
		}
		out = append(out, p)
	}
	return out
}

// metricGaps is withGaps for a metric series.
func metricGaps(points []metricPoint) []metricPoint {
	return withGaps(points,
		func(p metricPoint) int64 { return p.Timestamp },
		func(ts int64) metricPoint { return metricPoint{Timestamp: ts} })
}

func roundTo(v float64, places int) *float64 {
	scale := math.Pow(10, float64(places))
	r := math.Round(v*scale) / scale
	return &r
}
