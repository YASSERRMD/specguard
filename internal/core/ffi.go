package core

/*
#cgo LDFLAGS: -L${SRCDIR}/../../rust/target/release -lspecguard_ffi -ldl -lm -lpthread
#include <stdlib.h>

char* hash_spec(const char* spec_json);
char* diff_specs(const char* spec_a_json, const char* spec_b_json);
void free_string(char* s);
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// HashSpec computes the structural hash of a specification using the Rust FFI.
func HashSpec(spec *NormalizedSpec) (string, error) {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec for hashing: %w", err)
	}

	cSpecJSON := C.CString(string(specJSON))
	defer C.free(unsafe.Pointer(cSpecJSON))

	cHash := C.hash_spec(cSpecJSON)
	if cHash == nil {
		return "", fmt.Errorf("spec hashing FFI call failed")
	}
	defer C.free_string(cHash)

	return C.GoString(cHash), nil
}

// DiffSpecs computes the structural drift between two specifications using the Rust FFI.
func DiffSpecs(specA, specB *NormalizedSpec) (*DriftReport, error) {
	specAJSON, err := json.Marshal(specA)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal spec A for diffing: %w", err)
	}
	specBJSON, err := json.Marshal(specB)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal spec B for diffing: %w", err)
	}

	cSpecAJSON := C.CString(string(specAJSON))
	defer C.free(unsafe.Pointer(cSpecAJSON))

	cSpecBJSON := C.CString(string(specBJSON))
	defer C.free(unsafe.Pointer(cSpecBJSON))

	cReport := C.diff_specs(cSpecAJSON, cSpecBJSON)
	if cReport == nil {
		return nil, fmt.Errorf("spec diffing FFI call failed")
	}
	defer C.free_string(cReport)

	var report DriftReport
	if err := json.Unmarshal([]byte(C.GoString(cReport)), &report); err != nil {
		return nil, fmt.Errorf("failed to unmarshal FFI drift report: %w", err)
	}

	return &report, nil
}
