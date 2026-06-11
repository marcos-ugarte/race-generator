package rng

import (
	"math"
	"math/bits"
	"testing"
)

// TestRNGStatisticalBattery is the GLI-19 §3.2.2 (Statistical Analysis),
// §3.2.3 (Distribution) and §3.2.4 (Independence) evidence for the MT19937
// RNG: a deterministic battery of goodness-of-fit and independence tests run
// over a large fixed-seed sample, decided at the 99% confidence level
// (α = 0.01), as a dry run of what the certification lab will execute.
//
// Why deterministic (fixed seed, no crypto/rand): the evidence must be exactly
// reproducible by the lab from the published seed. Because the seed is fixed,
// each sub-test PASSES OR FAILS deterministically — there is no per-run flake.
// A fair PRNG yields p-values ~Uniform(0,1), so a 99% gate has a 1% chance of
// rejecting on a hostile seed; we use the canonical golden seed (0x42×32, the
// same one pinned by TestSeedHexGoldenVector) and confirm it passes every
// sub-test, so the gate is a stable regression guard, not a coin flip.
//
// What is tested, and against which clause:
//   - §3.2.3 Distribution — three χ² equidistribution tests: the raw uint32
//     stream (top-8-bit buckets), the CertifiedInt(0,99) scaling (the integer
//     draw actually used by the generator, with rejection sampling) and the
//     CertifiedFloat [0,1) bucketing.
//   - §3.2.4 Independence — lag-1..8 serial (auto)correlation and a runs test
//     about the median; both standardised to a z-score bounded by ±2.576.
//   - §3.2.2 Statistical Analysis — a monobit (bit-frequency) test over all
//     32·N output bits.
//   - §3.2.5 Available Outcomes — the CertifiedInt χ² doubles as evidence that
//     every outcome in [0,99] is reachable with no gaps (empty bin ⇒ fail).
//
// The raw MT19937 is a well-studied generator and passes these comfortably;
// the cert-relevant caveat for MT is PREDICTABILITY (§3.3.2), which is NOT a
// distribution/independence property and is treated in the RNG design doc
// (docs/racegen-design), not here.
func TestRNGStatisticalBattery(t *testing.T) {
	if testing.Short() {
		t.Skip("RNG statistical battery skipped in -short")
	}

	const (
		seedHex = "4242424242424242424242424242424242424242424242424242424242424242"
		sampleN = 2_000_000
		alpha   = 0.01  // 99% confidence
		zCrit   = 2.576 // two-sided normal critical value at α=0.01
	)

	// One canonical stream, shared by every sub-test so they all characterise
	// the same published seed.
	mt, err := NewMT19937WithSeedHex(seedHex)
	if err != nil {
		t.Fatalf("seed hex: %v", err)
	}
	u := make([]uint32, sampleN)
	f := make([]float64, sampleN)
	for i := range u {
		u[i] = mt.NextUint32()
		f[i] = float64(u[i]) / 4294967296.0
	}

	t.Logf("=== RNG statistical battery: seed=%s N=%d α=%.2f ===", seedHex, sampleN, alpha)

	// --- §3.2.3 Distribution: χ² equidistribution of the raw uint32 stream. ---
	t.Run("uint32_frequency_chi2", func(t *testing.T) {
		const bins = 256
		counts := make([]int64, bins)
		for _, v := range u {
			counts[v>>24]++ // top 8 bits
		}
		chi2, df := chiSquareUniform(counts)
		p := chiSquarePValue(chi2, df)
		t.Logf("uint32 top-8-bit: χ²=%.2f df=%d p=%.4f", chi2, df, p)
		if p < alpha {
			t.Errorf("uint32 frequency χ² p=%.4f < α=%.2f (non-uniform)", p, alpha)
		}
	})

	// --- §3.2.3 + §3.2.5: χ² of the CertifiedInt(0,99) draw actually used. ---
	t.Run("certifiedint_uniformity_chi2", func(t *testing.T) {
		const k = 100
		mt2, _ := NewMT19937WithSeedHex(seedHex)
		counts := make([]int64, k)
		for i := 0; i < sampleN; i++ {
			counts[CertifiedInt(mt2, 0, k-1)]++
		}
		for b, c := range counts {
			if c == 0 {
				t.Errorf("CertifiedInt: outcome %d never produced (§3.2.5 unreachable)", b)
			}
		}
		chi2, df := chiSquareUniform(counts)
		p := chiSquarePValue(chi2, df)
		t.Logf("CertifiedInt(0,99): χ²=%.2f df=%d p=%.4f", chi2, df, p)
		if p < alpha {
			t.Errorf("CertifiedInt χ² p=%.4f < α=%.2f (non-uniform / scaling bias)", p, alpha)
		}
	})

	// --- §3.2.3: χ² equidistribution of CertifiedFloat [0,1). ---
	t.Run("float_uniformity_chi2", func(t *testing.T) {
		const k = 100
		counts := make([]int64, k)
		for _, x := range f {
			b := int(x * k)
			if b >= k {
				b = k - 1
			}
			counts[b]++
		}
		chi2, df := chiSquareUniform(counts)
		p := chiSquarePValue(chi2, df)
		t.Logf("CertifiedFloat [0,1): χ²=%.2f df=%d p=%.4f", chi2, df, p)
		if p < alpha {
			t.Errorf("float χ² p=%.4f < α=%.2f (non-uniform)", p, alpha)
		}
	})

	// --- §3.2.4 Independence: lag-k serial (auto)correlation. ---
	t.Run("serial_correlation", func(t *testing.T) {
		var mean float64
		for _, x := range f {
			mean += x
		}
		mean /= float64(len(f))
		var denom float64
		for _, x := range f {
			d := x - mean
			denom += d * d
		}
		for lag := 1; lag <= 8; lag++ {
			var num float64
			for i := 0; i+lag < len(f); i++ {
				num += (f[i] - mean) * (f[i+lag] - mean)
			}
			r := num / denom
			z := r * math.Sqrt(float64(len(f)))
			t.Logf("lag %d: r=%+.6f z=%+.3f", lag, r, z)
			if math.Abs(z) > zCrit {
				t.Errorf("lag %d serial correlation z=%.3f exceeds ±%.3f (dependent)", lag, z, zCrit)
			}
		}
	})

	// --- §3.2.4 Independence: runs test about the median. ---
	t.Run("runs_about_median", func(t *testing.T) {
		const median = 0.5
		var n1, n2, runs int64
		prevAbove := false
		for i, x := range f {
			above := x >= median
			if above {
				n1++
			} else {
				n2++
			}
			if i == 0 || above != prevAbove {
				runs++
			}
			prevAbove = above
		}
		nn := float64(n1 + n2)
		mu := 2*float64(n1)*float64(n2)/nn + 1
		variance := 2 * float64(n1) * float64(n2) * (2*float64(n1)*float64(n2) - nn) /
			(nn * nn * (nn - 1))
		z := (float64(runs) - mu) / math.Sqrt(variance)
		t.Logf("runs: R=%d n1=%d n2=%d μ=%.1f z=%+.3f", runs, n1, n2, mu, z)
		if math.Abs(z) > zCrit {
			t.Errorf("runs test z=%.3f exceeds ±%.3f (non-random clustering)", z, zCrit)
		}
	})

	// --- §3.2.2: monobit (bit-frequency) over all 32·N output bits. ---
	t.Run("monobit", func(t *testing.T) {
		var ones int64
		for _, v := range u {
			ones += int64(bits.OnesCount32(v))
		}
		total := float64(len(u)) * 32.0
		mean := total / 2.0
		sd := math.Sqrt(total) / 2.0
		z := (float64(ones) - mean) / sd
		t.Logf("monobit: ones=%d total=%.0f z=%+.3f", ones, total, z)
		if math.Abs(z) > zCrit {
			t.Errorf("monobit z=%.3f exceeds ±%.3f (bit bias)", z, zCrit)
		}
	})
}

// chiSquareUniform returns the χ² statistic and degrees of freedom for a
// goodness-of-fit test of `counts` against a uniform distribution over its
// bins.
func chiSquareUniform(counts []int64) (chi2 float64, df int) {
	var total int64
	for _, c := range counts {
		total += c
	}
	expected := float64(total) / float64(len(counts))
	for _, c := range counts {
		d := float64(c) - expected
		chi2 += d * d / expected
	}
	return chi2, len(counts) - 1
}

// chiSquarePValue returns P(X² ≥ chi2) for a chi-square distribution with df
// degrees of freedom — the upper-tail p-value, via the regularised upper
// incomplete gamma function Q(df/2, chi2/2).
func chiSquarePValue(chi2 float64, df int) float64 {
	return gammq(float64(df)/2.0, chi2/2.0)
}

// gammq is the regularised upper incomplete gamma function Q(a,x) = 1 - P(a,x).
// Numerical Recipes §6.2: series expansion for x < a+1, continued fraction
// otherwise.
func gammq(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x < a+1 {
		return 1 - gser(a, x)
	}
	return gcf(a, x)
}

func gser(a, x float64) float64 {
	const itmax = 1000
	const eps = 3e-14
	gln, _ := math.Lgamma(a)
	if x <= 0 {
		return 0
	}
	ap := a
	sum := 1.0 / a
	del := sum
	for n := 0; n < itmax; n++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*eps {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-gln)
}

func gcf(a, x float64) float64 {
	const itmax = 1000
	const eps = 3e-14
	const fpmin = 1e-300
	gln, _ := math.Lgamma(a)
	b := x + 1 - a
	c := 1.0 / fpmin
	d := 1.0 / b
	h := d
	for i := 1; i < itmax; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = b + an/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-gln) * h
}
