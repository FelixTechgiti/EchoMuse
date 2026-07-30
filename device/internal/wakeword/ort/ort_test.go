package ort_test

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wilbowes/EchoMuse/internal/wakeword"
	"github.com/wilbowes/EchoMuse/internal/wakeword/ort"
)

// These tests need the ONNX Runtime library and the three model files, none of
// which live in this repository — the library is 12MB and is distributed to
// devices separately, and the models come from openwakeword. So they SKIP
// unless pointed at them:
//
//	EM_ORT_LIB=/usr/local/lib/python3.10/dist-packages/onnxruntime/capi/libonnxruntime.so.1.23.2 \
//	EM_OWW_MODELS=/usr/local/lib/python3.10/dist-packages/openwakeword/resources/models \
//	EM_OWW_CLASSIFIER=/path/to/hey_mycroft_v0.1.onnx \
//	go test ./internal/wakeword/ort/
//
// Skipping rather than failing is deliberate: CI would otherwise need a 12MB
// download to test a package whose whole design point is that it degrades
// gracefully when the library is absent. TestOpenMissingLibraryFails covers
// that path, and it needs nothing.
func models(t *testing.T) (lib string, m ort.Models) {
	t.Helper()
	lib = os.Getenv("EM_ORT_LIB")
	dir := os.Getenv("EM_OWW_MODELS")
	cls := os.Getenv("EM_OWW_CLASSIFIER")
	if lib == "" || dir == "" || cls == "" {
		t.Skip("set EM_ORT_LIB, EM_OWW_MODELS and EM_OWW_CLASSIFIER to run the ONNX Runtime tests")
	}
	return lib, ort.Models{
		Melspec:    filepath.Join(dir, "melspectrogram.onnx"),
		Embedding:  filepath.Join(dir, "embedding_model.onnx"),
		Classifier: cls,
	}
}

func newInferer(t *testing.T) *ort.Inferer {
	t.Helper()
	lib, m := models(t)
	rt, err := ort.Open(lib)
	if err != nil {
		t.Fatalf("Open(%s): %v", lib, err)
	}
	t.Logf("onnxruntime %s", rt.Version())
	inf, err := rt.NewInferer(m, ort.DefaultOptions())
	if err != nil {
		t.Fatalf("NewInferer: %v", err)
	}
	t.Cleanup(func() { inf.Close() })
	t.Logf("xnnpack active: %v", inf.XNNPACKActive())
	return inf
}

// ortFixture mirrors testdata/ort_fixture.bin — see gen_ort_fixture.py. Unlike
// the parent package's fixture it carries its own input audio, so there is no
// second implementation of a random generator to drift out of step with
// Python's.
type ortFixture struct {
	audio []int16
	recs  []struct {
		melInLen int
		melOut   []float32
		emb      []float32
		score    float32
	}
	embProbeIn, embProbeOut []float32
	clsProbeIn              []float32
	clsProbeOut             float32
}

func loadORTFixture(t *testing.T) *ortFixture {
	t.Helper()
	raw, err := os.ReadFile("../testdata/ort_fixture.bin")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if string(raw[:8]) != "OWWORT02" {
		t.Fatalf("bad fixture magic %q", raw[:8])
	}
	p := 8
	u32 := func() int { v := binary.LittleEndian.Uint32(raw[p:]); p += 4; return int(v) }
	f32n := func(n int) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[p:]))
			p += 4
		}
		return out
	}

	f := &ortFixture{}
	nSamples := u32()
	f.audio = make([]int16, nSamples)
	for i := range f.audio {
		f.audio[i] = int16(binary.LittleEndian.Uint16(raw[p:]))
		p += 2
	}
	f.recs = make([]struct {
		melInLen int
		melOut   []float32
		emb      []float32
		score    float32
	}, u32())
	for i := range f.recs {
		f.recs[i].melInLen = u32()
		frames := u32()
		f.recs[i].melOut = f32n(frames * wakeword.MelBins)
		f.recs[i].emb = f32n(wakeword.FeatDim)
		f.recs[i].score = f32n(1)[0]
	}
	f.embProbeIn = f32n(wakeword.MelWindow * wakeword.MelBins)
	f.embProbeOut = f32n(wakeword.FeatDim)
	f.clsProbeIn = f32n(wakeword.FeatWindow * wakeword.FeatDim)
	f.clsProbeOut = f32n(1)[0]
	if p != len(raw) {
		t.Fatalf("fixture not fully consumed: %d of %d bytes", p, len(raw))
	}
	return f
}

// Tolerance for cross-engine comparison. ARM and x86 were measured to agree to
// ~7 significant figures on these models, which is float32 epsilon plus SIMD
// reassociation, so 1e-4 relative is generous by two orders of magnitude — and
// still six orders below anything that could move a wake decision against a
// 0.5 threshold. The absolute floor keeps near-zero values (most of a
// melspectrogram) from failing on meaningless relative error.
const (
	relTol = 1e-4
	absTol = 1e-5
)

func closeEnough(got, want float32) bool {
	d := math.Abs(float64(got - want))
	if d <= absTol {
		return true
	}
	return d <= relTol*math.Abs(float64(want))
}

func compare(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d values, want %d", label, len(got), len(want))
		return
	}
	bad := 0
	for i := range got {
		if !closeEnough(got[i], want[i]) {
			if bad < 4 {
				t.Errorf("%s[%d] = %v, Python gave %v", label, i, got[i], want[i])
			}
			bad++
		}
	}
	if bad > 4 {
		t.Errorf("%s: %d of %d values differ beyond tolerance", label, bad, len(got))
	}
}

// spyInferer wraps the real Inferer so the test can see the tensors flowing
// between the models while the parent package drives them. The point is to
// exercise the REAL composition — wakeword.Detector calling into ONNX Runtime —
// rather than poking the three models by hand, because the interesting failure
// is a mismatch between the two halves, not inside either.
type spyInferer struct {
	inner   *ort.Inferer
	melOuts [][]float32
	embs    [][]float32
}

func (s *spyInferer) Melspec(samples []float32) ([]float32, int, error) {
	out, frames, err := s.inner.Melspec(samples)
	if err == nil {
		// The Inferer reuses its output buffer, so this has to be a copy.
		s.melOuts = append(s.melOuts, append([]float32(nil), out...))
	}
	return out, frames, err
}

func (s *spyInferer) Embed(window []float32) ([]float32, error) {
	out, err := s.inner.Embed(window)
	if err == nil {
		s.embs = append(s.embs, append([]float32(nil), out...))
	}
	return out, err
}

func (s *spyInferer) Classify(feats []float32) (float32, error) { return s.inner.Classify(feats) }

// TestInfererMatchesPython is the end-to-end claim: the Go pipeline driving
// real ONNX Runtime reproduces what openWakeWord's Python produced from the
// same audio, at every stage.
//
// The parent package's fixture proves the BUFFERING by replaying model
// outputs; this proves the INFERENCE by running the models for real. Neither
// alone is sufficient — replayed models cannot catch a wrong tensor shape or a
// misconfigured session, and hand-fed models cannot catch a misaligned window.
func TestInfererMatchesPython(t *testing.T) {
	fx := loadORTFixture(t)
	spy := &spyInferer{inner: newInferer(t)}
	d := wakeword.New(spy)

	scores := make([]float32, 0, len(fx.recs))
	for i := 0; i < len(fx.recs); i++ {
		chunk := fx.audio[i*wakeword.ChunkSamples : (i+1)*wakeword.ChunkSamples]
		n, err := d.Push(chunk)
		if err != nil {
			t.Fatalf("chunk %d: Push: %v", i, err)
		}
		if n != 1 {
			t.Fatalf("chunk %d: produced %d embeddings, want 1", i, n)
		}
		if !d.Ready() {
			scores = append(scores, math.MaxFloat32) // sentinel, not compared
			continue
		}
		s, err := d.Score()
		if err != nil {
			t.Fatalf("chunk %d: Score: %v", i, err)
		}
		scores = append(scores, s)
	}

	// Melspectrogram output, per chunk. This is the stage most sensitive to a
	// wrong input length: 1280 samples on the first chunk and 1760 after,
	// yielding 5 then 8 frames.
	if len(spy.melOuts) != len(fx.recs) {
		t.Fatalf("melspec ran %d times, want %d", len(spy.melOuts), len(fx.recs))
	}
	for i, rec := range fx.recs {
		if want := rec.melInLen; want != wakeword.ChunkSamples && want != wakeword.ChunkSamples+wakeword.MelContextSamples {
			t.Fatalf("fixture chunk %d has an unexpected melspec input length %d", i, want)
		}
		compare(t, "melspec chunk "+strconv.Itoa(i), spy.melOuts[i], rec.melOut)
	}

	// Embeddings, per chunk, from chunk 0: the mel ring's 1.0 pre-fill is
	// deterministic, so these must agree immediately.
	for i, rec := range fx.recs {
		compare(t, "embedding chunk "+strconv.Itoa(i), spy.embs[i], rec.emb)
	}

	// Scores only once the feature ring holds real audio. Before that Python's
	// ring still contains embeddings of its random warm-up audio while ours is
	// clean — the deliberate divergence documented in the parent package.
	for i := wakeword.FeatWindow; i < len(fx.recs); i++ {
		if !closeEnough(scores[i], fx.recs[i].score) {
			t.Errorf("score chunk %d = %v, Python gave %v", i, scores[i], fx.recs[i].score)
		}
	}
}

// TestProbeTensorsMatchPython covers the two model stages the audio path
// exercises weakly. On synthetic noise every score is 0.0000, so a classifier
// wired to return a constant zero would pass the test above; the probe feeds a
// wide-range tensor that produces a small but distinctly non-zero score.
func TestProbeTensorsMatchPython(t *testing.T) {
	fx := loadORTFixture(t)
	inf := newInferer(t)

	emb, err := inf.Embed(fx.embProbeIn)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	compare(t, "embedding probe", emb, fx.embProbeOut)

	score, err := inf.Classify(fx.clsProbeIn)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if fx.clsProbeOut == 0 {
		t.Fatal("fixture probe score is zero, which would make this test vacuous")
	}
	if !closeEnough(score, fx.clsProbeOut) {
		t.Errorf("classifier probe = %v, Python gave %v", score, fx.clsProbeOut)
	}
}

// TestRejectsWrongTensorSizes guards the shape checks. ONNX Runtime would
// otherwise be handed a tensor whose declared shape disagrees with its data
// length, which reads out of bounds rather than failing cleanly.
func TestRejectsWrongTensorSizes(t *testing.T) {
	inf := newInferer(t)
	if _, err := inf.Embed(make([]float32, wakeword.MelWindow*wakeword.MelBins-1)); err == nil {
		t.Error("Embed accepted a short window")
	}
	if _, err := inf.Classify(make([]float32, wakeword.FeatWindow*wakeword.FeatDim+1)); err == nil {
		t.Error("Classify accepted an oversized feature tensor")
	}
	if _, _, err := inf.Melspec(nil); err == nil {
		t.Error("Melspec accepted no samples")
	}
}

// TestUseAfterCloseFails pins that a closed Inferer reports an error rather
// than calling into a released session.
func TestUseAfterCloseFails(t *testing.T) {
	lib, m := models(t)
	rt, err := ort.Open(lib)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	inf, err := rt.NewInferer(m, ort.DefaultOptions())
	if err != nil {
		t.Fatalf("NewInferer: %v", err)
	}
	if err := inf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := inf.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := inf.Embed(make([]float32, wakeword.MelWindow*wakeword.MelBins)); !errors.Is(err, ort.ErrClosed) {
		t.Errorf("Embed after Close returned %v, want ErrClosed", err)
	}
	if _, err := inf.Classify(make([]float32, wakeword.FeatWindow*wakeword.FeatDim)); !errors.Is(err, ort.ErrClosed) {
		t.Errorf("Classify after Close returned %v, want ErrClosed", err)
	}
}

// TestOpenMissingLibraryFails is the one test here that needs no ONNX Runtime,
// and it covers the property the whole dlopen design exists for: a device with
// no libonnxruntime.so must get a clean error, not a link failure at exec and
// not a crash. The firmware keeps running with controller-side wake word.
func TestOpenMissingLibraryFails(t *testing.T) {
	_, err := ort.Open(filepath.Join(t.TempDir(), "libonnxruntime.so"))
	if err == nil {
		t.Fatal("Open succeeded for a nonexistent library")
	}
	if !strings.Contains(err.Error(), "ort: open") {
		t.Errorf("error %q does not identify itself as an ort open failure", err)
	}
}

// TestThreadsMustBePositive covers the one Options value that cannot be
// defaulted silently: ORT treats 0 as "pick for me", which would quietly
// undo the single-thread choice that keeps CPU at 36% instead of 63%.
func TestThreadsMustBePositive(t *testing.T) {
	lib, m := models(t)
	rt, err := ort.Open(lib)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	o := ort.DefaultOptions()
	o.Threads = 0
	if _, err := rt.NewInferer(m, o); err == nil {
		t.Error("NewInferer accepted Threads=0")
	}
}

