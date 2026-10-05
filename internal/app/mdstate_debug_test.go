package app

import (
	"fmt"
	"testing"
)

func TestDebugMDState(t *testing.T) {
	src := "| Field | Type |\n|-------|------|\n| attempts | `atomic.Int32` |\n| deadline | time.Time |\n\n> quoted note\n\n---\n\n1. first item\n2. second item"
	st := &mdState{}
	st.feed("", src)
	st.line(st.pending + "\n")
	st.pending = ""
	for i, p := range st.parts {
		fmt.Printf("part %d: code=%v lang=%q text=%q\n", i, p.code, p.lang, p.text)
	}
}
