package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/MojoHand70/fadodb/fado"
)

// The actions the program carries out itself (ADR-0102, items 1 to 4):
// the library calls the program's function with the call, as a webhook
// gets it, and the function answers with fado_answer before it returns.

// host is the program's function as Go calls it: the number of the call,
// the action and the call in JSON. main.go makes one of a function of C.
type host func(call uint64, action, object string)

// A call while the program's function runs, by its number. A number is
// given once in the life of the process, as a database's is.
type pending struct {
	answer   []byte
	answered bool
}

var (
	callsMu  sync.Mutex
	calls    = map[uint64]*pending{}
	lastCall uint64
)

// answerCall is fado_answer: 0 when the answer was taken, -1 for a call
// that was answered already or has returned.
func answerCall(n uint64, answer string) int {
	callsMu.Lock()
	defer callsMu.Unlock()
	p := calls[n]
	if p == nil || p.answered {
		return -1
	}
	p.answer, p.answered = []byte(answer), true
	return 0
}

// actionsOf gives each action the options name to the program's function.
func actionsOf(names []string, f host) map[string]fado.ActionFunc {
	actions := make(map[string]fado.ActionFunc, len(names))
	for _, name := range names {
		actions[name] = carriedOut(f)
	}
	return actions
}

// carriedOut is an action the program's function carries out.
func carriedOut(f host) fado.ActionFunc {
	return func(_ context.Context, call *fado.ActionCall) (fado.Values, error) {
		object, err := json.Marshal(call)
		if err != nil {
			return nil, fmt.Errorf("the call of %s does not encode: %w", call.Action, err)
		}
		callsMu.Lock()
		lastCall++
		n := lastCall
		p := &pending{}
		calls[n] = p
		callsMu.Unlock()
		defer func() {
			callsMu.Lock()
			delete(calls, n)
			callsMu.Unlock()
		}()

		f(n, call.Action, string(object))

		callsMu.Lock()
		answer, answered := p.answer, p.answered
		p.answered = true // a late fado_answer is refused
		callsMu.Unlock()
		if !answered {
			return nil, errors.New("the program's function for " + call.Action + " returned without answering with fado_answer")
		}
		return readAnswer(call.Action, answer)
	}
}

// readAnswer reads what the program's function answered: the result of
// the action, or its failure, a refusal when it is permanent.
func readAnswer(action string, answer []byte) (fado.Values, error) {
	var told struct {
		Result fado.Values `json:"result"`
		Error  *struct {
			Message   string `json:"message"`
			Permanent bool   `json:"permanent"`
		} `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(answer))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&told); err != nil {
		return nil, fmt.Errorf("the program's function for %s answered %s, which is no {\"result\": …} and no {\"error\": …}: %v", action, short(answer), err)
	}
	switch {
	case told.Error != nil && told.Result != nil:
		return nil, fmt.Errorf("the program's function for %s answered both a result and an error", action)
	case told.Error != nil:
		message := told.Error.Message
		if message == "" {
			message = "the program's function for " + action + " failed and said nothing"
		}
		if told.Error.Permanent {
			return nil, fado.Permanent(errors.New(message))
		}
		return nil, errors.New(message)
	}
	if told.Result == nil {
		told.Result = fado.Values{}
	}
	return told.Result, nil
}

func short(b []byte) string {
	if len(b) > 200 {
		return strconv.Quote(string(b[:200])) + "…"
	}
	return strconv.Quote(string(b))
}
