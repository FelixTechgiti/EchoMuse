// Package fixture reads the golden captures in wakeword/testdata and defines
// what it means for two inference engines to agree.
//
// It exists because two consumers need the same answer from the same bytes:
// the host test in wakeword/ort, and the on-device probe in
// device/tools/oww_probe. A format with a magic number, a strict
// fully-consumed check and a tolerance policy is exactly the thing that should
// not be written twice — a probe that parsed the file slightly differently, or
// compared with a slightly looser tolerance, would report agreement the test
// would not.
package fixture

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/wilbowes/EchoMuse/internal/wakeword"
)

// Record is one 80ms chunk's worth of what Python computed.
type Record struct {
	// MelInLen is how many samples were handed to the melspectrogram model:
	// 1280 on the first chunk (nothing precedes it) and 1760 after, once 480
	// samples of left context exist.
	MelInLen int
	// MelOut is the model's RAW output, before the /10+2 transform the
	// streaming pipeline applies.
	MelOut []float32
	Emb    []float32
	Score  float32
}

// ORT is testdata/ort_fixture.bin: input audio plus per-chunk model outputs,
// plus a probe block. See testdata/gen_ort_fixture.py for the format and for
// why the audio is carried in the file rather than regenerated.
type ORT struct {
	Audio   []int16
	Records []Record

	// The probe block covers what the audio path exercises weakly. Synthetic
	// noise scores 0.0000 at every chunk, so a classifier hard-wired to return
	// zero passes on the audio alone; these are one wide-range tensor per
	// model, whose classifier score is small but distinctly non-zero.
	EmbProbeIn, EmbProbeOut []float32
	ClsProbeIn              []float32
	ClsProbeOut             float32
}

const ortMagic = "OWWORT02"

// LoadORT reads and validates the fixture. Every size is derived from the
// file's own headers and checked against the model geometry, and the whole
// file must be consumed — a fixture that parses but leaves bytes over means
// the writer and reader disagree, which is worth an error rather than a
// silently truncated comparison.
func LoadORT(path string) (*ORT, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < len(ortMagic)+4 || string(raw[:len(ortMagic)]) != ortMagic {
		return nil, fmt.Errorf("fixture %s: bad magic (want %s)", path, ortMagic)
	}

	p := len(ortMagic)
	// need reports whether n more bytes are available, so a truncated file
	// produces an error instead of a slice-bounds panic.
	need := func(n int) error {
		if p+n > len(raw) {
			return fmt.Errorf("fixture %s: truncated at byte %d, wanted %d more", path, p, n)
		}
		return nil
	}
	u32 := func() (int, error) {
		if err := need(4); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint32(raw[p:])
		p += 4
		return int(v), nil
	}
	f32n := func(n int) ([]float32, error) {
		if err := need(4 * n); err != nil {
			return nil, err
		}
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[p:]))
			p += 4
		}
		return out, nil
	}

	f := &ORT{}
	nSamples, err := u32()
	if err != nil {
		return nil, err
	}
	if err := need(2 * nSamples); err != nil {
		return nil, err
	}
	f.Audio = make([]int16, nSamples)
	for i := range f.Audio {
		f.Audio[i] = int16(binary.LittleEndian.Uint16(raw[p:]))
		p += 2
	}

	nRecs, err := u32()
	if err != nil {
		return nil, err
	}
	f.Records = make([]Record, nRecs)
	for i := range f.Records {
		if f.Records[i].MelInLen, err = u32(); err != nil {
			return nil, err
		}
		frames, err := u32()
		if err != nil {
			return nil, err
		}
		if f.Records[i].MelOut, err = f32n(frames * wakeword.MelBins); err != nil {
			return nil, err
		}
		if f.Records[i].Emb, err = f32n(wakeword.FeatDim); err != nil {
			return nil, err
		}
		score, err := f32n(1)
		if err != nil {
			return nil, err
		}
		f.Records[i].Score = score[0]
	}

	if f.EmbProbeIn, err = f32n(wakeword.MelWindow * wakeword.MelBins); err != nil {
		return nil, err
	}
	if f.EmbProbeOut, err = f32n(wakeword.FeatDim); err != nil {
		return nil, err
	}
	if f.ClsProbeIn, err = f32n(wakeword.FeatWindow * wakeword.FeatDim); err != nil {
		return nil, err
	}
	clsOut, err := f32n(1)
	if err != nil {
		return nil, err
	}
	f.ClsProbeOut = clsOut[0]

	if p != len(raw) {
		return nil, fmt.Errorf("fixture %s: %d of %d bytes consumed", path, p, len(raw))
	}
	if nSamples < len(f.Records)*wakeword.ChunkSamples {
		return nil, fmt.Errorf("fixture %s: %d samples cannot supply %d chunks",
			path, nSamples, len(f.Records))
	}
	return f, nil
}

// Chunk returns the i'th 80ms slice of audio.
func (f *ORT) Chunk(i int) []int16 {
	return f.Audio[i*wakeword.ChunkSamples : (i+1)*wakeword.ChunkSamples]
}

// Tolerance for comparing one inference engine against another.
//
// ARM and x86 were measured to agree to ~7 significant figures on these
// models, which is float32 epsilon plus SIMD reassociation, so 1e-4 relative
// is generous by two orders of magnitude — and still six orders below anything
// that could move a wake decision against a 0.5 threshold. The absolute floor
// keeps near-zero values (most of a melspectrogram) from failing on
// meaningless relative error.
const (
	RelTol = 1e-4
	AbsTol = 1e-5
)

// CloseEnough reports whether two values agree within tolerance.
func CloseEnough(got, want float32) bool {
	d := math.Abs(float64(got - want))
	if d <= AbsTol {
		return true
	}
	return d <= RelTol*math.Abs(float64(want))
}

// Diff describes how two tensors disagree. Zero value means they match.
type Diff struct {
	// N is how many values are outside tolerance.
	N int
	// Worst is the largest absolute difference seen, and WorstAt where.
	Worst   float64
	WorstAt int
	// Examples holds up to four human-readable mismatches, so a caller can
	// report something useful without dumping thousands of lines.
	Examples []string
}

// Ok reports whether the tensors agreed.
func (d Diff) Ok() bool { return d.N == 0 }

// Compare checks got against want elementwise. A length mismatch is reported
// as a single mismatch rather than a panic, because on a real device the
// interesting version of that bug is a model whose output shape is not what
// the pipeline assumed.
func Compare(got, want []float32) Diff {
	var d Diff
	if len(got) != len(want) {
		d.N = 1
		d.Examples = append(d.Examples, fmt.Sprintf("length %d, want %d", len(got), len(want)))
		return d
	}
	for i := range got {
		delta := math.Abs(float64(got[i] - want[i]))
		if delta > d.Worst {
			d.Worst, d.WorstAt = delta, i
		}
		if !CloseEnough(got[i], want[i]) {
			d.N++
			if len(d.Examples) < 4 {
				d.Examples = append(d.Examples,
					fmt.Sprintf("[%d] = %v, want %v", i, got[i], want[i]))
			}
		}
	}
	return d
}

// spy wraps an Inferer to capture the tensors flowing between the models while
// the real pipeline drives them. Copies are taken because implementations are
// free to reuse their output buffers, and ort.Inferer does.
type spy struct {
	inner    wakeword.Inferer
	melInLen []int
	melOuts  [][]float32
	embs     [][]float32
}

func (s *spy) Melspec(samples []float32) ([]float32, int, error) {
	out, frames, err := s.inner.Melspec(samples)
	if err == nil {
		s.melInLen = append(s.melInLen, len(samples))
		s.melOuts = append(s.melOuts, append([]float32(nil), out...))
	}
	return out, frames, err
}

func (s *spy) Embed(window []float32) ([]float32, error) {
	out, err := s.inner.Embed(window)
	if err == nil {
		s.embs = append(s.embs, append([]float32(nil), out...))
	}
	return out, err
}

func (s *spy) Classify(feats []float32) (float32, error) { return s.inner.Classify(feats) }

// Report is the outcome of Verify: how an inference engine compared with
// Python at every stage, per 80ms chunk.
type Report struct {
	Melspec   []Diff
	Embedding []Diff
	Score     []Diff

	// ScoreFrom is the first chunk whose score is comparable. Python seeds its
	// feature ring with embeddings of random warm-up audio; the Go pipeline
	// starts clean and reports not-ready instead, so scores only become
	// comparable once the ring holds nothing but real audio.
	ScoreFrom int

	// Structural holds failures that are not numerical — a wrong number of
	// melspectrogram calls, an unexpected input length, a chunk that produced
	// the wrong number of embeddings. These matter more than a tolerance miss:
	// they mean the pipeline and the models disagree about shape.
	Structural []string

	// Err is set if the run could not be completed at all.
	Err error
}

// Ok reports whether the engine reproduced Python everywhere.
func (r Report) Ok() bool {
	if r.Err != nil || len(r.Structural) > 0 {
		return false
	}
	for _, set := range [][]Diff{r.Melspec, r.Embedding, r.Score} {
		for _, d := range set {
			if !d.Ok() {
				return false
			}
		}
	}
	return true
}

// Summary renders the report as a few lines of text, for a diagnostic tool
// with no testing.T to report through.
func (r Report) Summary() string {
	if r.Err != nil {
		return "FAIL: " + r.Err.Error()
	}
	stage := func(name string, set []Diff) string {
		bad, worst := 0, 0.0
		for _, d := range set {
			if !d.Ok() {
				bad++
			}
			if d.Worst > worst {
				worst = d.Worst
			}
		}
		verdict := "ok"
		if bad > 0 {
			verdict = fmt.Sprintf("FAIL (%d chunks differ)", bad)
		}
		return fmt.Sprintf("  %-10s %-24s max abs diff %.3g\n", name, verdict, worst)
	}
	out := stage("melspec", r.Melspec) + stage("embedding", r.Embedding) +
		stage("score", r.Score[r.ScoreFrom:])
	for _, s := range r.Structural {
		out += "  STRUCTURAL: " + s + "\n"
	}
	// The first mismatch of each stage is worth showing: a systematic error
	// looks different from a single outlier, and the distinction decides
	// whether to suspect the models or the buffering.
	for name, set := range map[string][]Diff{"melspec": r.Melspec, "embedding": r.Embedding, "score": r.Score} {
		for i, d := range set {
			if !d.Ok() {
				out += fmt.Sprintf("  first %s mismatch, chunk %d: %v\n", name, i, d.Examples)
				break
			}
		}
	}
	return out
}

// Verify drives the real streaming pipeline over the fixture's audio using the
// given engine, and compares every stage against what Python produced.
//
// This lives here, rather than in either caller, because the host test and the
// on-device probe must ask exactly the same question. A probe that verified
// slightly less than the test would report success the test would not, and it
// is the probe whose answer gets trusted — it runs on the hardware.
func Verify(inf wakeword.Inferer, fx *ORT) Report {
	s := &spy{inner: inf}
	d := wakeword.New(s)
	r := Report{ScoreFrom: wakeword.FeatWindow}

	scores := make([]float32, len(fx.Records))
	for i := range fx.Records {
		n, err := d.Push(fx.Chunk(i))
		if err != nil {
			r.Err = fmt.Errorf("chunk %d: push: %w", i, err)
			return r
		}
		if n != 1 {
			r.Structural = append(r.Structural,
				fmt.Sprintf("chunk %d produced %d embeddings, want 1", i, n))
		}
		if !d.Ready() {
			continue
		}
		if scores[i], err = d.Score(); err != nil {
			r.Err = fmt.Errorf("chunk %d: score: %w", i, err)
			return r
		}
	}

	if len(s.melOuts) != len(fx.Records) {
		r.Structural = append(r.Structural,
			fmt.Sprintf("melspec ran %d times, want %d", len(s.melOuts), len(fx.Records)))
		return r
	}
	for i, rec := range fx.Records {
		if s.melInLen[i] != rec.MelInLen {
			r.Structural = append(r.Structural, fmt.Sprintf(
				"chunk %d fed melspec %d samples, Python fed %d",
				i, s.melInLen[i], rec.MelInLen))
		}
		r.Melspec = append(r.Melspec, Compare(s.melOuts[i], rec.MelOut))
		r.Embedding = append(r.Embedding, Compare(s.embs[i], rec.Emb))

		var d Diff
		if i >= r.ScoreFrom {
			d = Compare([]float32{scores[i]}, []float32{rec.Score})
		}
		r.Score = append(r.Score, d)
	}
	return r
}
