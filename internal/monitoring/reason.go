package monitoring

import (
	"strconv"

	"github.com/lanory/lanory/internal/model"
)

// This file turns a state decision into two artefacts:
//
//   - the English sentence, for logs, bug reports and API consumers outside
//     the web UI;
//   - the reason code and its parameters, so the web UI can render the same
//     verdict in the user's language.
//
// Both come from the same decision, so they cannot drift: every branch below
// states the code, the parameters and the sentence together.

// probeReason holds the three things a verdict needs: the code, the parameters
// the message template refers to, and the English sentence.
type probeReason struct {
	code   model.ReasonCode
	params map[string]string
	text   string
}

// newReason builds a probe reason.
func newReason(code model.ReasonCode, text string, params map[string]string) probeReason {
	if params == nil {
		params = map[string]string{}
	}
	return probeReason{code: code, params: params, text: text}
}

// Reason returns the translatable form of the verdict.
func (r probeReason) Reason() *model.Reason {
	return &model.Reason{Code: r.code, Params: r.params}
}

// Text returns the English sentence.
func (r probeReason) Text() string { return r.text }

// commonParams are the parameters every probe reason carries: where the probe
// ran from and how it reached the device.
func commonParams(src, proto string) map[string]string {
	return map[string]string{"source": src, "protocol": proto}
}

// withParams merges extra parameters into the common ones.
func withParams(p map[string]string, extra map[string]string) map[string]string {
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// itoa renders an int for a reason parameter.
func itoa(n int) string { return strconv.Itoa(n) }
