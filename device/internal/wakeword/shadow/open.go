package shadow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wilbowes/EchoMuse/internal/wakeword/ort"
)

// DefaultDir is where the ONNX Runtime library and the models live on the
// device. Overridable with EM_OWW_DIR, mainly so the same binary can be
// pointed at a scratch directory while iterating.
//
// The library is NOT part of the firmware: it is 12.3MB, it changes far less
// often than the binary, and embedding it would double both the OTA payload and
// the space taken by the A/B slots. It is installed out of band, and its
// absence is an ordinary, expected condition — see Open.
const DefaultDir = "/data/local/share/echomuse/oww"

// Dir returns the configured model directory.
func Dir() string {
	if d := os.Getenv("EM_OWW_DIR"); d != "" {
		return d
	}
	return DefaultDir
}

// ModelStem turns the controller's owwModel value into the classifier filename
// stem. That value is a bare name for built-in models ("hey_jarvis_v0.1") and a
// FILE PATH for custom ones, and openwakeword itself keys predictions by the
// filename stem in both cases — so the device has to reduce it the same way or
// it will look for a file that cannot exist.
func ModelStem(owwModel string) string {
	stem := filepath.Base(strings.TrimSpace(owwModel))
	return strings.TrimSuffix(stem, filepath.Ext(stem))
}

// Open loads ONNX Runtime and the three models for owwModel, and starts a
// Scorer.
//
// A missing library or model returns an error and is NOT a failure of the
// device: shadow mode is off until someone installs them, the caller logs it
// once and carries on with controller-side wake word. That is the whole reason
// the runtime is dlopen'd rather than linked.
func Open(owwModel string, threshold float32, onCross func(score float32, at time.Time)) (*Scorer, error) {
	dir := Dir()
	stem := ModelStem(owwModel)
	if stem == "" {
		return nil, fmt.Errorf("shadow: no wake word model configured")
	}

	models := ort.Models{
		Melspec:    filepath.Join(dir, "melspectrogram.onnx"),
		Embedding:  filepath.Join(dir, "embedding_model.onnx"),
		Classifier: filepath.Join(dir, stem+".onnx"),
	}
	// Checked up front so the log names the missing file. ORT's own error for a
	// bad path mentions neither which of the three it was nor what was expected
	// there, and this is a path people will get wrong while installing by hand.
	for what, p := range map[string]string{
		"melspectrogram model": models.Melspec,
		"embedding model":      models.Embedding,
		"classifier model":     models.Classifier,
	} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("shadow: %s not installed at %s", what, p)
		}
	}

	lib := filepath.Join(dir, "libonnxruntime.so")
	rt, err := ort.Open(lib)
	if err != nil {
		return nil, fmt.Errorf("shadow: %w", err)
	}
	inf, err := rt.NewInferer(models, ort.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("shadow: %w", err)
	}

	s := NewScorer(inf, threshold, onCross)
	s.closer = inf
	s.info = fmt.Sprintf("onnxruntime %s, model %s, xnnpack=%v",
		rt.Version(), stem, inf.XNNPACKActive())
	return s, nil
}
