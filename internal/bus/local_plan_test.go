package bus

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	aa28Address    = byte(0x75)
	aa28Subscriber = uint64(11)
	aa28Epoch      = uint64(21)
	aa28Worker     = uint64(31)
	aa28Stream     = uint64(41)
	aa28PlanGen    = uint64(51)
	aa28SourceGen  = uint64(61)
	aa28PlanExpiry = uint64(1500)
	aa28RegExpiry  = uint64(1600)
	aa28SrcExpiry  = uint64(1700)
	aa28Domain     = uint64(7)
	aa28Tick       = uint64(1000)
)

var aa28Silence = LocalFailurePolicy{
	Unavailable: LocalFailureSilence,
	Malformed:   LocalFailureSilence,
	Timeout:     LocalFailureSilence,
}

func aa28Contract() LocalPlanContract {
	return LocalPlanContract{
		Revision:                1,
		MaxEntries:              4,
		MaxRequestBytes:         8,
		MaxEncodedResponseBytes: 40,
		MaxPlanBytes:            96,
		MaxLeaseTicks:           1000,
	}
}

func aa28Profile() LocalProfile {
	return LocalProfile{
		ID:      "synthetic-read-only",
		Address: aa28Address,
		Failure: aa28Silence,
		Entries: []LocalProfileEntry{
			{
				Source:          LocalSourceSelector{Any: true},
				PB:              0x07,
				SB:              0x04,
				Operation:       LocalOperationRead,
				MaxEncodedBytes: 8,
				Reply:           []byte{0x02},
			},
			{
				Source:          LocalSourceSelector{Source: 0x10},
				PB:              0x50,
				SB:              0x22,
				RequestLength:   2,
				Request:         []byte{0x0A, 0x0B},
				Operation:       LocalOperationRead,
				MaxEncodedBytes: 12,
				Reply:           []byte{0xA9, 0xAA, 0x07},
			},
		},
	}
}

func aa28Binding() LocalPlanBinding {
	return LocalPlanBinding{
		Subscriber: aa28Subscriber,
		Epoch:      aa28Epoch,
		Worker:     aa28Worker,
		Stream:     aa28Stream,
		Plan:       aa28PlanGen,
		Source:     aa28SourceGen,
		PlanExpiry: aa28PlanExpiry,
	}
}

func aa28Key() LocalRegistrationKey {
	return LocalRegistrationKey{Subscriber: aa28Subscriber, Address: aa28Address, Epoch: aa28Epoch}
}

func aa28View() LocalRegistrationView {
	return LocalRegistrationView{
		Key:               aa28Key(),
		State:             LocalRegistrationActive,
		LeaseExpiry:       aa28RegExpiry,
		Source:            aa28SourceGen,
		SourceLeaseExpiry: aa28SrcExpiry,
	}
}

func aa28Input() LocalPlanInput {
	return LocalPlanInput{
		Profile:      aa28Profile(),
		Binding:      aa28Binding(),
		Contract:     aa28Contract(),
		Registration: aa28View(),
		Now:          LocalClockReading{Domain: aa28Domain, Tick: aa28Tick},
	}
}

// aa28CRC is an independent bitwise eBUS CRC over wire-expanded symbols.
func aa28CRC(logical []byte) byte {
	step := func(crc, value byte) byte {
		for bit := 0; bit < 8; bit++ {
			if crc&0x80 != 0 {
				crc = crc<<1 ^ 0x9B
			} else {
				crc <<= 1
			}
		}
		return crc ^ value
	}
	crc := byte(0)
	for _, value := range logical {
		switch value {
		case 0xA9:
			crc = step(step(crc, 0xA9), 0x00)
		case 0xAA:
			crc = step(step(crc, 0xA9), 0x01)
		default:
			crc = step(crc, value)
		}
	}
	return crc
}

// aa28DecodeResponse parses LEN DATA CRC from the wire without the production
// encoder and fails on a raw SYN, broken escape, wrong length or wrong CRC.
func aa28DecodeResponse(t *testing.T, wire []byte) []byte {
	t.Helper()
	logical := make([]byte, 0, len(wire))
	for offset := 0; offset < len(wire); offset++ {
		value := wire[offset]
		switch value {
		case 0xAA:
			t.Fatalf("raw SYN in %x", wire)
		case 0xA9:
			offset++
			if offset >= len(wire) {
				t.Fatalf("truncated escape in %x", wire)
			}
			switch wire[offset] {
			case 0x00:
				value = 0xA9
			case 0x01:
				value = 0xAA
			default:
				t.Fatalf("bad escape in %x", wire)
			}
		}
		logical = append(logical, value)
	}
	if len(logical) < 2 || int(logical[0]) != len(logical)-2 {
		t.Fatalf("bad length in %x", wire)
	}
	last := len(logical) - 1
	if logical[last] != aa28CRC(logical[:last]) {
		t.Fatalf("bad CRC in %x", wire)
	}
	return logical[1:last]
}

// aa28WantPlan is written out field by field so it shares nothing with the
// compiler. The first reply's wire form is a fixed vector.
func aa28WantPlan() LocalPlan {
	key := aa28Key()
	escapedCRC := aa28CRC([]byte{0x03, 0xA9, 0xAA, 0x07})
	return LocalPlan{
		ProfileID: "synthetic-read-only",
		Identity: LocalPlanIdentity{
			Registration: key,
			Worker:       aa28Worker,
			Stream:       aa28Stream,
			Plan:         aa28PlanGen,
			Source:       aa28SourceGen,
		},
		Validity: LocalPlanValidity{
			Domain:             aa28Domain,
			PlanExpiry:         aa28PlanExpiry,
			RegistrationExpiry: aa28RegExpiry,
			SourceLeaseExpiry:  aa28SrcExpiry,
		},
		Failure: aa28Silence,
		Entries: []LocalPlanEntry{
			{
				Source:          LocalSourceSelector{Any: true},
				Destination:     aa28Address,
				PB:              0x07,
				SB:              0x04,
				Operation:       LocalOperationRead,
				Phase:           LocalPhaseResponse,
				MaxEncodedBytes: 8,
				EncodedResponse: []byte{0x01, 0x02, 0x99},
			},
			{
				Source:          LocalSourceSelector{Source: 0x10},
				Destination:     aa28Address,
				PB:              0x50,
				SB:              0x22,
				RequestLength:   2,
				Request:         []byte{0x0A, 0x0B},
				Operation:       LocalOperationRead,
				Phase:           LocalPhaseResponse,
				MaxEncodedBytes: 12,
				EncodedResponse: []byte{0x03, 0xA9, 0x00, 0xA9, 0x01, 0x07, escapedCRC},
			},
		},
	}
}

func aa28WantEqual(t *testing.T, got, want LocalPlan) {
	t.Helper()
	if !reflect.DeepEqual(aa28Normal(got), aa28Normal(want)) {
		t.Fatalf("plan mismatch\n got %+v\nwant %+v", got, want)
	}
}

// aa28Normal treats nil and empty byte slices alike; both are an empty request.
func aa28Normal(plan LocalPlan) LocalPlan {
	clone := plan.Clone()
	for index := range clone.Entries {
		if len(clone.Entries[index].Request) == 0 {
			clone.Entries[index].Request = nil
		}
	}
	return clone
}

func TestAA28CompilesBoundedReadOnlyProfile(t *testing.T) {
	first, err := CompileLocalPlan(aa28Input())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	aa28WantEqual(t, first, aa28WantPlan())
	second, err := CompileLocalPlan(aa28Input())
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	aa28WantEqual(t, second, first)
	if got := aa28DecodeResponse(t, first.Entries[1].EncodedResponse); !bytes.Equal(got, []byte{0xA9, 0xAA, 0x07}) {
		t.Fatalf("decoded reply %x", got)
	}
}

// Each case changes one field of the input, keeps the rest valid, and states the
// one field of the expected plan that must follow it.
func TestAA28EveryIdentityFieldReachesThePlanIndependently(t *testing.T) {
	cases := []struct {
		name  string
		input func(*LocalPlanInput)
		want  func(*LocalPlan)
	}{
		{"profile", func(in *LocalPlanInput) { in.Profile.ID = "other" }, func(p *LocalPlan) { p.ProfileID = "other" }},
		{"address", func(in *LocalPlanInput) {
			in.Profile.Address = 0x76
			in.Registration.Key.Address = 0x76
		}, func(p *LocalPlan) {
			p.Identity.Registration.Address = 0x76
			p.Entries[0].Destination = 0x76
			p.Entries[1].Destination = 0x76
		}},
		{"subscriber", func(in *LocalPlanInput) {
			in.Binding.Subscriber = 12
			in.Registration.Key.Subscriber = 12
		}, func(p *LocalPlan) { p.Identity.Registration.Subscriber = 12 }},
		{"registration epoch", func(in *LocalPlanInput) {
			in.Binding.Epoch = 22
			in.Registration.Key.Epoch = 22
		}, func(p *LocalPlan) { p.Identity.Registration.Epoch = 22 }},
		{"worker", func(in *LocalPlanInput) { in.Binding.Worker = 32 }, func(p *LocalPlan) { p.Identity.Worker = 32 }},
		{"stream", func(in *LocalPlanInput) { in.Binding.Stream = 42 }, func(p *LocalPlan) { p.Identity.Stream = 42 }},
		{"plan epoch", func(in *LocalPlanInput) { in.Binding.Plan = 52 }, func(p *LocalPlan) { p.Identity.Plan = 52 }},
		{"source epoch", func(in *LocalPlanInput) {
			in.Binding.Source = 62
			in.Registration.Source = 62
		}, func(p *LocalPlan) { p.Identity.Source = 62 }},
		{"plan expiry", func(in *LocalPlanInput) { in.Binding.PlanExpiry = 1501 }, func(p *LocalPlan) { p.Validity.PlanExpiry = 1501 }},
		{"registration lease", func(in *LocalPlanInput) { in.Registration.LeaseExpiry = 1601 }, func(p *LocalPlan) { p.Validity.RegistrationExpiry = 1601 }},
		{"source lease", func(in *LocalPlanInput) { in.Registration.SourceLeaseExpiry = 1701 }, func(p *LocalPlan) { p.Validity.SourceLeaseExpiry = 1701 }},
		{"clock domain", func(in *LocalPlanInput) { in.Now.Domain = 8 }, func(p *LocalPlan) { p.Validity.Domain = 8 }},
		{"source selector", func(in *LocalPlanInput) { in.Profile.Entries[1].Source.Source = 0x30 }, func(p *LocalPlan) { p.Entries[1].Source.Source = 0x30 }},
		{"any selector", func(in *LocalPlanInput) { in.Profile.Entries[1].Source = LocalSourceSelector{Any: true} }, func(p *LocalPlan) { p.Entries[1].Source = LocalSourceSelector{Any: true} }},
		{"primary", func(in *LocalPlanInput) { in.Profile.Entries[1].PB = 0x51 }, func(p *LocalPlan) { p.Entries[1].PB = 0x51 }},
		{"secondary", func(in *LocalPlanInput) { in.Profile.Entries[1].SB = 0x23 }, func(p *LocalPlan) { p.Entries[1].SB = 0x23 }},
		{"request bytes", func(in *LocalPlanInput) { in.Profile.Entries[1].Request = []byte{0x0A, 0x0C} }, func(p *LocalPlan) { p.Entries[1].Request = []byte{0x0A, 0x0C} }},
		{"request length", func(in *LocalPlanInput) {
			in.Profile.Entries[1].RequestLength = 3
			in.Profile.Entries[1].Request = []byte{0x0A, 0x0B, 0x0C}
		}, func(p *LocalPlan) {
			p.Entries[1].RequestLength = 3
			p.Entries[1].Request = []byte{0x0A, 0x0B, 0x0C}
		}},
		{"reply payload", func(in *LocalPlanInput) { in.Profile.Entries[0].Reply = []byte{0x01, 0x02} }, func(p *LocalPlan) {
			p.Entries[0].EncodedResponse = []byte{0x02, 0x01, 0x02, aa28CRC([]byte{0x02, 0x01, 0x02})}
		}},
		{"encoded bound", func(in *LocalPlanInput) { in.Profile.Entries[1].MaxEncodedBytes = 13 }, func(p *LocalPlan) { p.Entries[1].MaxEncodedBytes = 13 }},
	}
	base := aa28WantPlan()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := aa28Input()
			tc.input(&input)
			got, err := CompileLocalPlan(input)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			want := aa28WantPlan()
			tc.want(&want)
			if reflect.DeepEqual(aa28Normal(want), aa28Normal(base)) {
				t.Fatal("case does not vary the plan")
			}
			aa28WantEqual(t, got, want)
		})
	}
}

type aa28Refusal struct {
	name   string
	mutate func(*LocalPlanInput)
	want   error
}

func aa28Refusals() []aa28Refusal {
	entry := func(index int, mutate func(*LocalProfileEntry)) func(*LocalPlanInput) {
		return func(in *LocalPlanInput) { mutate(&in.Profile.Entries[index]) }
	}
	extra := func(mutate func(*LocalProfileEntry)) func(*LocalPlanInput) {
		return func(in *LocalPlanInput) {
			added := in.Profile.Entries[1]
			mutate(&added)
			in.Profile.Entries = append(in.Profile.Entries, added)
		}
	}
	return []aa28Refusal{
		{"stateful entry", entry(1, func(e *LocalProfileEntry) { e.Operation = LocalOperationStateful }), ErrLocalPlanNotReadOnly},
		{"stateful last entry", extra(func(e *LocalProfileEntry) { e.PB = 0x60; e.Operation = LocalOperationStateful }), ErrLocalPlanNotReadOnly},
		{"unspecified operation", entry(0, func(e *LocalProfileEntry) { e.Operation = LocalOperationUnspecified }), ErrLocalPlanNotReadOnly},
		{"unknown operation", entry(0, func(e *LocalProfileEntry) { e.Operation = 99 }), ErrLocalPlanNotReadOnly},

		{"nack when unavailable", func(in *LocalPlanInput) { in.Profile.Failure.Unavailable = LocalFailureNACK }, ErrLocalPlanUnsupportedFailure},
		{"nack when malformed", func(in *LocalPlanInput) { in.Profile.Failure.Malformed = LocalFailureNACK }, ErrLocalPlanUnsupportedFailure},
		{"nack on timeout", func(in *LocalPlanInput) { in.Profile.Failure.Timeout = LocalFailureNACK }, ErrLocalPlanUnsupportedFailure},
		{"unspecified unavailable", func(in *LocalPlanInput) { in.Profile.Failure.Unavailable = LocalFailureUnspecified }, ErrLocalPlanUnsupportedFailure},
		{"unspecified malformed", func(in *LocalPlanInput) { in.Profile.Failure.Malformed = LocalFailureUnspecified }, ErrLocalPlanUnsupportedFailure},
		{"unspecified timeout", func(in *LocalPlanInput) { in.Profile.Failure.Timeout = LocalFailureUnspecified }, ErrLocalPlanUnsupportedFailure},

		// 0xA9 0xAA 0x07 is 3 logical bytes but 7 on the wire: a bound of 6 passes
		// any check on the logical or unescaped length.
		{"encoded over entry bound", entry(1, func(e *LocalProfileEntry) { e.MaxEncodedBytes = 6 }), ErrLocalPlanOversize},
		{"entry bound over contract", entry(1, func(e *LocalProfileEntry) { e.MaxEncodedBytes = 41 }), ErrLocalPlanOversize},
		{"zero entry bound", entry(1, func(e *LocalProfileEntry) { e.MaxEncodedBytes = 0 }), ErrLocalPlanInvalidProfile},
		{"reply over native length", entry(1, func(e *LocalProfileEntry) { e.Reply = make([]byte, 256) }), ErrLocalPlanOversize},
		{"request over contract", entry(1, func(e *LocalProfileEntry) {
			e.Request = make([]byte, 9)
			e.RequestLength = 9
		}), ErrLocalPlanOversize},
		{"too many entries", func(in *LocalPlanInput) {
			in.Profile.Entries = append(in.Profile.Entries, in.Profile.Entries...)
			in.Profile.Entries = append(in.Profile.Entries, in.Profile.Entries[0])
		}, ErrLocalPlanOversize},
		{"plan bytes over contract", func(in *LocalPlanInput) {
			in.Contract.MaxPlanBytes = 48
			in.Profile.Entries = []LocalProfileEntry{
				{Source: LocalSourceSelector{Any: true}, PB: 1, Operation: LocalOperationRead, MaxEncodedBytes: 40, Reply: []byte(strings.Repeat("r", 22))},
				{Source: LocalSourceSelector{Any: true}, PB: 2, RequestLength: 1, Request: []byte{0x01}, Operation: LocalOperationRead, MaxEncodedBytes: 40, Reply: []byte(strings.Repeat("r", 22))},
			}
		}, ErrLocalPlanOversize},

		{"no entries", func(in *LocalPlanInput) { in.Profile.Entries = nil }, ErrLocalPlanInvalidProfile},
		{"empty profile id", func(in *LocalPlanInput) { in.Profile.ID = "" }, ErrLocalPlanInvalidProfile},
		{"long profile id", func(in *LocalPlanInput) { in.Profile.ID = strings.Repeat("x", 65) }, ErrLocalPlanInvalidProfile},
		{"declared length under request", entry(1, func(e *LocalProfileEntry) { e.RequestLength = 1 }), ErrLocalPlanInvalidProfile},
		{"declared length over request", entry(1, func(e *LocalProfileEntry) { e.RequestLength = 3 }), ErrLocalPlanInvalidProfile},
		{"any selector with source", entry(0, func(e *LocalProfileEntry) { e.Source = LocalSourceSelector{Any: true, Source: 0x10} }), ErrLocalPlanInvalidProfile},
		{"target as source", entry(1, func(e *LocalProfileEntry) { e.Source = LocalSourceSelector{Source: 0x75} }), ErrLocalPlanInvalidProfile},

		{"duplicate exact selector", extra(func(e *LocalProfileEntry) { e.Reply = []byte{0x09} }), ErrLocalPlanAmbiguous},
		{"any over exact selector", extra(func(e *LocalProfileEntry) { e.Source = LocalSourceSelector{Any: true} }), ErrLocalPlanAmbiguous},
		{"exact over any selector", func(in *LocalPlanInput) {
			added := in.Profile.Entries[0]
			added.Source = LocalSourceSelector{Source: 0x10}
			in.Profile.Entries = append(in.Profile.Entries, added)
		}, ErrLocalPlanAmbiguous},

		{"zero subscriber", func(in *LocalPlanInput) { in.Binding.Subscriber = 0; in.Registration.Key.Subscriber = 0 }, ErrLocalPlanInvalidIdentity},
		{"zero epoch", func(in *LocalPlanInput) { in.Binding.Epoch = 0; in.Registration.Key.Epoch = 0 }, ErrLocalPlanInvalidIdentity},
		{"zero worker", func(in *LocalPlanInput) { in.Binding.Worker = 0 }, ErrLocalPlanInvalidIdentity},
		{"zero stream", func(in *LocalPlanInput) { in.Binding.Stream = 0 }, ErrLocalPlanInvalidIdentity},
		{"zero plan", func(in *LocalPlanInput) { in.Binding.Plan = 0 }, ErrLocalPlanInvalidIdentity},
		{"zero source", func(in *LocalPlanInput) { in.Binding.Source = 0; in.Registration.Source = 0 }, ErrLocalPlanInvalidIdentity},

		{"broadcast address", func(in *LocalPlanInput) { in.Profile.Address = 0xFE; in.Registration.Key.Address = 0xFE }, ErrLocalPlanInvalidAddress},
		{"escape address", func(in *LocalPlanInput) { in.Profile.Address = 0xA9; in.Registration.Key.Address = 0xA9 }, ErrLocalPlanInvalidAddress},
		{"syn address", func(in *LocalPlanInput) { in.Profile.Address = 0xAA; in.Registration.Key.Address = 0xAA }, ErrLocalPlanInvalidAddress},
		{"initiator address", func(in *LocalPlanInput) { in.Profile.Address = 0x10; in.Registration.Key.Address = 0x10 }, ErrLocalPlanInvalidAddress},

		{"contract revision zero", func(in *LocalPlanInput) { in.Contract.Revision = 0 }, ErrLocalPlanContractMismatch},
		{"contract revision ahead", func(in *LocalPlanInput) { in.Contract.Revision = 2 }, ErrLocalPlanContractMismatch},
		{"contract no entries", func(in *LocalPlanInput) { in.Contract.MaxEntries = 0 }, ErrLocalPlanContractMismatch},
		{"contract no request", func(in *LocalPlanInput) { in.Contract.MaxRequestBytes = 0 }, ErrLocalPlanContractMismatch},
		{"contract no encoded", func(in *LocalPlanInput) { in.Contract.MaxEncodedResponseBytes = 0 }, ErrLocalPlanContractMismatch},
		{"contract no plan bytes", func(in *LocalPlanInput) { in.Contract.MaxPlanBytes = 0 }, ErrLocalPlanContractMismatch},
		{"contract no lease", func(in *LocalPlanInput) { in.Contract.MaxLeaseTicks = 0 }, ErrLocalPlanContractMismatch},
		{"contract request over native", func(in *LocalPlanInput) {
			in.Contract.MaxRequestBytes = 256
			in.Contract.MaxPlanBytes = 1024
		}, ErrLocalPlanContractMismatch},
		{"contract encoded over native", func(in *LocalPlanInput) {
			in.Contract.MaxEncodedResponseBytes = 515
			in.Contract.MaxPlanBytes = 1024
		}, ErrLocalPlanContractMismatch},
		{"contract plan under one entry", func(in *LocalPlanInput) { in.Contract.MaxPlanBytes = 47 }, ErrLocalPlanContractMismatch},

		{"absent registration", func(in *LocalPlanInput) { in.Registration = LocalRegistrationView{} }, ErrLocalPlanUnknownRegistration},
		{"unknown registration state", func(in *LocalPlanInput) { in.Registration.State = 9 }, ErrLocalPlanUnknownRegistration},
		{"retiring registration", func(in *LocalPlanInput) { in.Registration.State = LocalRegistrationRetiring }, ErrLocalPlanConflict},
		{"known physical conflict", func(in *LocalPlanInput) { in.AddressConflict = true }, ErrLocalPlanConflict},
		{"view of another subscriber", func(in *LocalPlanInput) { in.Registration.Key.Subscriber++ }, ErrLocalPlanConflict},
		{"view of another address", func(in *LocalPlanInput) { in.Registration.Key.Address++ }, ErrLocalPlanConflict},
		{"view of another epoch", func(in *LocalPlanInput) { in.Registration.Key.Epoch++ }, ErrLocalPlanConflict},
		{"binding of another subscriber", func(in *LocalPlanInput) { in.Binding.Subscriber++ }, ErrLocalPlanConflict},
		{"binding of another epoch", func(in *LocalPlanInput) { in.Binding.Epoch++ }, ErrLocalPlanConflict},
		{"profile of another address", func(in *LocalPlanInput) { in.Profile.Address = 0x76 }, ErrLocalPlanConflict},
		{"binding source ahead", func(in *LocalPlanInput) { in.Binding.Source++ }, ErrLocalPlanSourceMismatch},
		{"registration source ahead", func(in *LocalPlanInput) { in.Registration.Source++ }, ErrLocalPlanSourceMismatch},

		{"zero clock domain", func(in *LocalPlanInput) { in.Now.Domain = 0 }, ErrLocalPlanClock},
		{"plan expiry reached", func(in *LocalPlanInput) { in.Binding.PlanExpiry = aa28Tick }, ErrLocalPlanExpired},
		{"registration lease reached", func(in *LocalPlanInput) { in.Registration.LeaseExpiry = aa28Tick }, ErrLocalPlanExpired},
		{"source lease reached", func(in *LocalPlanInput) { in.Registration.SourceLeaseExpiry = aa28Tick }, ErrLocalPlanExpired},
		{"plan lease over contract", func(in *LocalPlanInput) { in.Binding.PlanExpiry = aa28Tick + 1001 }, ErrLocalPlanLeaseTooLong},
	}
}

func TestAA28RefusesInvalidInputWithNoPlan(t *testing.T) {
	for _, tc := range aa28Refusals() {
		t.Run(tc.name, func(t *testing.T) {
			input := aa28Input()
			tc.mutate(&input)
			plan, err := CompileLocalPlan(input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(plan, LocalPlan{}) {
				t.Fatalf("refusal returned a plan: %+v", plan)
			}
		})
	}
}

// Valid controls right at each bound: without them the refusals above would also
// pass against a compiler that refuses too much.
func TestAA28BoundsAdmitTheirExactLimit(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*LocalPlanInput)
	}{
		{"encoded at entry bound", func(in *LocalPlanInput) { in.Profile.Entries[1].MaxEncodedBytes = 7 }},
		{"entry bound at contract", func(in *LocalPlanInput) { in.Profile.Entries[1].MaxEncodedBytes = 40 }},
		{"request at contract", func(in *LocalPlanInput) {
			in.Profile.Entries[1].Request = make([]byte, 8)
			in.Profile.Entries[1].RequestLength = 8
		}},
		{"entries at contract", func(in *LocalPlanInput) {
			for pb := byte(0x60); pb < 0x62; pb++ {
				added := in.Profile.Entries[1]
				added.PB = pb
				in.Profile.Entries = append(in.Profile.Entries, added)
			}
		}},
		{"plan bytes at contract", func(in *LocalPlanInput) {
			in.Contract.MaxPlanBytes = 48
			in.Profile.Entries = []LocalProfileEntry{
				{Source: LocalSourceSelector{Any: true}, PB: 1, Operation: LocalOperationRead, MaxEncodedBytes: 40, Reply: []byte(strings.Repeat("r", 22))},
				{Source: LocalSourceSelector{Any: true}, PB: 2, RequestLength: 1, Request: []byte{0x01}, Operation: LocalOperationRead, MaxEncodedBytes: 40, Reply: []byte(strings.Repeat("r", 21))},
			}
		}},
		{"plan lease at contract", func(in *LocalPlanInput) { in.Binding.PlanExpiry = aa28Tick + 1000 }},
		{"one tick before plan expiry", func(in *LocalPlanInput) { in.Binding.PlanExpiry = aa28Tick + 1 }},
		{"one tick before registration lease", func(in *LocalPlanInput) { in.Registration.LeaseExpiry = aa28Tick + 1 }},
		{"one tick before source lease", func(in *LocalPlanInput) { in.Registration.SourceLeaseExpiry = aa28Tick + 1 }},
		{"same selector from another source", func(in *LocalPlanInput) {
			added := in.Profile.Entries[1]
			added.Source = LocalSourceSelector{Source: 0x30}
			in.Profile.Entries = append(in.Profile.Entries, added)
		}},
		{"same selector with another request", func(in *LocalPlanInput) {
			added := in.Profile.Entries[1]
			added.Request = []byte{0x0A, 0x0C}
			in.Profile.Entries = append(in.Profile.Entries, added)
		}},
		{"same selector with another secondary", func(in *LocalPlanInput) {
			added := in.Profile.Entries[1]
			added.SB = 0x23
			in.Profile.Entries = append(in.Profile.Entries, added)
		}},
		{"initiator zero as exact source", func(in *LocalPlanInput) { in.Profile.Entries[1].Source = LocalSourceSelector{Source: 0x00} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := aa28Input()
			tc.mutate(&input)
			if _, err := CompileLocalPlan(input); err != nil {
				t.Fatalf("compile: %v", err)
			}
		})
	}
}

func TestAA28WireFormMatchesNativeResponseForEveryOctet(t *testing.T) {
	contract := aa28Contract()
	contract.MaxEncodedResponseBytes = 514
	contract.MaxPlanBytes = 1024
	compile := func(reply []byte, bound uint32) (LocalPlan, error) {
		input := aa28Input()
		input.Contract = contract
		input.Profile.Entries = []LocalProfileEntry{{
			Source:          LocalSourceSelector{Any: true},
			PB:              0x07,
			SB:              0x04,
			Operation:       LocalOperationRead,
			MaxEncodedBytes: bound,
			Reply:           reply,
		}}
		return CompileLocalPlan(input)
	}
	for value := 0; value < 256; value++ {
		reply := []byte{byte(value)}
		plan, err := compile(reply, 8)
		if err != nil {
			t.Fatalf("octet %02x: %v", value, err)
		}
		if got := aa28DecodeResponse(t, plan.Entries[0].EncodedResponse); !bytes.Equal(got, reply) {
			t.Fatalf("octet %02x decoded to %x", value, got)
		}
	}
	empty, err := compile(nil, 8)
	if err != nil {
		t.Fatalf("empty reply: %v", err)
	}
	if !bytes.Equal(empty.Entries[0].EncodedResponse, []byte{0x00, 0x00}) {
		t.Fatalf("empty reply wire %x", empty.Entries[0].EncodedResponse)
	}

	// The largest native reply: LEN, 255 escaped symbols and the CRC symbols.
	longest := bytes.Repeat([]byte{0xAA}, 255)
	wireSize := 1 + 2*255 + len(aa28EscapedCRC(longest))
	if _, err := compile(longest, uint32(wireSize)); err != nil {
		t.Fatalf("longest reply at its size: %v", err)
	}
	if _, err := compile(longest, uint32(wireSize-1)); !errors.Is(err, ErrLocalPlanOversize) {
		t.Fatalf("longest reply one under its size: %v", err)
	}
}

// aa28EscapedCRC returns the wire symbols of the CRC of a response.
func aa28EscapedCRC(data []byte) []byte {
	crc := aa28CRC(append([]byte{byte(len(data))}, data...))
	switch crc {
	case 0xA9:
		return []byte{0xA9, 0x00}
	case 0xAA:
		return []byte{0xA9, 0x01}
	}
	return []byte{crc}
}

// aa28SpyInstaller records every call; it never opens anything.
type aa28SpyInstaller struct {
	contract      LocalPlanContract
	registrations map[LocalRegistrationKey]LocalRegistrationView
	conflict      bool
	installErr    error

	reads     int
	installed []LocalPlan
}

func aa28NewSpy() *aa28SpyInstaller {
	return &aa28SpyInstaller{
		contract:      aa28Contract(),
		registrations: map[LocalRegistrationKey]LocalRegistrationView{aa28Key(): aa28View()},
	}
}

func (spy *aa28SpyInstaller) LocalPlanContract() LocalPlanContract { return spy.contract }

func (spy *aa28SpyInstaller) LocalPlanRegistration(key LocalRegistrationKey) LocalRegistrationView {
	spy.reads++
	return spy.registrations[key]
}

func (spy *aa28SpyInstaller) LocalPlanAddressConflict(byte) bool {
	spy.reads++
	return spy.conflict
}

func (spy *aa28SpyInstaller) InstallLocalPlan(plan LocalPlan) error {
	if spy.installErr != nil {
		return spy.installErr
	}
	spy.installed = append(spy.installed, plan)
	return nil
}

func aa28NewPublisher(t *testing.T, spy *aa28SpyInstaller) *LocalPlanPublisher {
	t.Helper()
	publisher, err := NewLocalPlanPublisher(spy)
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	return publisher
}

func aa28Publish(t *testing.T, publisher *LocalPlanPublisher, binding LocalPlanBinding) LocalPlan {
	t.Helper()
	plan, err := publisher.Publish(aa28Profile(), binding, LocalClockReading{Domain: aa28Domain, Tick: aa28Tick})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return plan
}

func TestAA28PublisherRequiresAnInstaller(t *testing.T) {
	if _, err := NewLocalPlanPublisher(nil); !errors.Is(err, ErrLocalPlanNoInstaller) {
		t.Fatalf("err = %v", err)
	}
}

func TestAA28PublisherInstallsOneWholeGeneration(t *testing.T) {
	spy := aa28NewSpy()
	publisher := aa28NewPublisher(t, spy)
	if _, held := publisher.Current(aa28Address); held {
		t.Fatal("new publisher holds a plan")
	}
	plan := aa28Publish(t, publisher, aa28Binding())
	if len(spy.installed) != 1 {
		t.Fatalf("installed %d plans", len(spy.installed))
	}
	aa28WantEqual(t, spy.installed[0], aa28WantPlan())
	aa28WantEqual(t, plan, aa28WantPlan())
	current, held := publisher.Current(aa28Address)
	if !held {
		t.Fatal("no current plan")
	}
	aa28WantEqual(t, current, aa28WantPlan())
}

// Every refusal of the compiler is replayed through a publisher already holding a
// previous generation: no install, and that generation stays exactly as it was.
func TestAA28InvalidCompilationLeavesPreviousGenerationIntact(t *testing.T) {
	previousBinding := aa28Binding()
	previousBinding.Plan = aa28PlanGen - 1
	want := aa28WantPlan()
	want.Identity.Plan = aa28PlanGen - 1
	for _, tc := range aa28Refusals() {
		t.Run(tc.name, func(t *testing.T) {
			spy := aa28NewSpy()
			publisher := aa28NewPublisher(t, spy)
			aa28Publish(t, publisher, previousBinding)

			input := aa28Input()
			tc.mutate(&input)
			spy.contract = input.Contract
			spy.conflict = input.AddressConflict
			key := input.Binding.key(input.Profile.Address)
			spy.registrations = map[LocalRegistrationKey]LocalRegistrationView{key: input.Registration}

			plan, err := publisher.Publish(input.Profile, input.Binding, input.Now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(plan, LocalPlan{}) {
				t.Fatalf("refusal returned a plan: %+v", plan)
			}
			if len(spy.installed) != 1 {
				t.Fatalf("refusal installed: %d plans", len(spy.installed))
			}
			current, held := publisher.Current(aa28Address)
			if !held {
				t.Fatal("refusal dropped the previous generation")
			}
			aa28WantEqual(t, current, want)
		})
	}
}

func TestAA28ContractMismatchStopsBeforeAnyOtherRead(t *testing.T) {
	spy := aa28NewSpy()
	spy.contract.Revision = LocalPlanContractRevision + 1
	publisher := aa28NewPublisher(t, spy)
	_, err := publisher.Publish(aa28Profile(), aa28Binding(), LocalClockReading{Domain: aa28Domain, Tick: aa28Tick})
	if !errors.Is(err, ErrLocalPlanContractMismatch) {
		t.Fatalf("err = %v", err)
	}
	if spy.reads != 0 || len(spy.installed) != 0 {
		t.Fatalf("mismatch still read %d times and installed %d plans", spy.reads, len(spy.installed))
	}
}

func TestAA28InstallerRefusalKeepsPreviousGeneration(t *testing.T) {
	spy := aa28NewSpy()
	publisher := aa28NewPublisher(t, spy)
	aa28Publish(t, publisher, aa28Binding())

	refused := errors.New("synthetic pool refusal")
	spy.installErr = refused
	next := aa28Binding()
	next.Plan = aa28PlanGen + 1
	plan, err := publisher.Publish(aa28Profile(), next, LocalClockReading{Domain: aa28Domain, Tick: aa28Tick})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(plan, LocalPlan{}) {
		t.Fatalf("refusal returned a plan: %+v", plan)
	}
	current, _ := publisher.Current(aa28Address)
	aa28WantEqual(t, current, aa28WantPlan())

	// The refused generation was never held, so it can still be installed.
	spy.installErr = nil
	if plan := aa28Publish(t, publisher, next); plan.Identity.Plan != aa28PlanGen+1 {
		t.Fatalf("plan generation %d", plan.Identity.Plan)
	}
}

func TestAA28GenerationOrderVariesEachFieldIndependently(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*LocalPlanBinding, *LocalRegistrationView)
		want   error
	}{
		{"same plan", func(*LocalPlanBinding, *LocalRegistrationView) {}, ErrLocalPlanStaleGeneration},
		{"older plan", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Plan-- }, ErrLocalPlanStaleGeneration},
		{"newer plan", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Plan++ }, nil},
		{"older worker", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Worker--; b.Plan++ }, ErrLocalPlanStaleGeneration},
		{"newer worker restarts plans", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Worker++; b.Plan = 1 }, nil},
		{"older stream", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Stream--; b.Plan++ }, ErrLocalPlanStaleGeneration},
		{"newer stream keeps plan order", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Stream++ }, ErrLocalPlanStaleGeneration},
		{"newer stream newer plan", func(b *LocalPlanBinding, _ *LocalRegistrationView) { b.Stream++; b.Plan++ }, nil},
		{"older source", func(b *LocalPlanBinding, v *LocalRegistrationView) {
			b.Plan++
			b.Source--
			v.Source--
		}, ErrLocalPlanStaleGeneration},
		{"newer source", func(b *LocalPlanBinding, v *LocalRegistrationView) {
			b.Plan++
			b.Source++
			v.Source++
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := aa28NewSpy()
			publisher := aa28NewPublisher(t, spy)
			first := aa28Publish(t, publisher, aa28Binding())

			next := aa28Binding()
			view := aa28View()
			tc.mutate(&next, &view)
			spy.registrations[aa28Key()] = view
			plan, err := publisher.Publish(aa28Profile(), next, LocalClockReading{Domain: aa28Domain, Tick: aa28Tick})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			current, _ := publisher.Current(aa28Address)
			if tc.want != nil {
				aa28WantEqual(t, current, first)
				return
			}
			aa28WantEqual(t, current, plan)
			if plan.Identity != (LocalPlanIdentity{
				Registration: aa28Key(), Worker: next.Worker, Stream: next.Stream, Plan: next.Plan, Source: next.Source,
			}) {
				t.Fatalf("identity %+v", plan.Identity)
			}
		})
	}
}

func TestAA28AnotherKeyTakesAnAddressOnlyAfterRelease(t *testing.T) {
	cases := []struct {
		name string
		key  func(*LocalPlanBinding)
	}{
		{"subscriber", func(b *LocalPlanBinding) { b.Subscriber++ }},
		{"epoch", func(b *LocalPlanBinding) { b.Epoch++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := aa28NewSpy()
			publisher := aa28NewPublisher(t, spy)
			first := aa28Publish(t, publisher, aa28Binding())

			next := aa28Binding()
			tc.key(&next)
			next.Plan++
			nextKey := next.key(aa28Address)
			view := aa28View()
			view.Key = nextKey
			spy.registrations[nextKey] = view

			now := LocalClockReading{Domain: aa28Domain, Tick: aa28Tick}
			if _, err := publisher.Publish(aa28Profile(), next, now); !errors.Is(err, ErrLocalPlanConflict) {
				t.Fatalf("incumbent active: err = %v", err)
			}
			current, _ := publisher.Current(aa28Address)
			aa28WantEqual(t, current, first)

			retiring := aa28View()
			retiring.State = LocalRegistrationRetiring
			spy.registrations[aa28Key()] = retiring
			if _, err := publisher.Publish(aa28Profile(), next, now); !errors.Is(err, ErrLocalPlanConflict) {
				t.Fatalf("incumbent retiring: err = %v", err)
			}
			if len(spy.installed) != 1 {
				t.Fatalf("conflict installed: %d plans", len(spy.installed))
			}

			delete(spy.registrations, aa28Key())
			plan, err := publisher.Publish(aa28Profile(), next, now)
			if err != nil {
				t.Fatalf("incumbent released: %v", err)
			}
			if plan.Identity.Registration != nextKey {
				t.Fatalf("registration %+v", plan.Identity.Registration)
			}
		})
	}
}

func TestAA28PublisherKeepsAddressesApart(t *testing.T) {
	spy := aa28NewSpy()
	publisher := aa28NewPublisher(t, spy)
	first := aa28Publish(t, publisher, aa28Binding())

	other := aa28Profile()
	other.Address = 0x76
	otherKey := LocalRegistrationKey{Subscriber: aa28Subscriber, Address: 0x76, Epoch: aa28Epoch}
	view := aa28View()
	view.Key = otherKey
	spy.registrations[otherKey] = view
	binding := aa28Binding()
	binding.Plan = aa28PlanGen + 1
	if _, err := publisher.Publish(other, binding, LocalClockReading{Domain: aa28Domain, Tick: aa28Tick}); err != nil {
		t.Fatalf("publish other: %v", err)
	}
	current, _ := publisher.Current(aa28Address)
	aa28WantEqual(t, current, first)
	otherPlan, held := publisher.Current(0x76)
	if !held || otherPlan.Identity.Registration != otherKey {
		t.Fatalf("other plan %+v held=%v", otherPlan.Identity, held)
	}
}

func aa28Scribble(plan LocalPlan) {
	for index := range plan.Entries {
		for offset := range plan.Entries[index].Request {
			plan.Entries[index].Request[offset] ^= 0xFF
		}
		for offset := range plan.Entries[index].EncodedResponse {
			plan.Entries[index].EncodedResponse[offset] ^= 0xFF
		}
		plan.Entries[index].PB ^= 0xFF
	}
}

func TestAA28CompiledPlanOwnsItsBytes(t *testing.T) {
	input := aa28Input()
	plan, err := CompileLocalPlan(input)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for index := range input.Profile.Entries {
		for offset := range input.Profile.Entries[index].Request {
			input.Profile.Entries[index].Request[offset] ^= 0xFF
		}
		for offset := range input.Profile.Entries[index].Reply {
			input.Profile.Entries[index].Reply[offset] ^= 0xFF
		}
	}
	aa28WantEqual(t, plan, aa28WantPlan())
}

func TestAA28EveryPublishedCopyIsDetached(t *testing.T) {
	cases := []struct {
		name     string
		scribble func(t *testing.T, spy *aa28SpyInstaller, returned LocalPlan, publisher *LocalPlanPublisher)
	}{
		{"installed copy", func(_ *testing.T, spy *aa28SpyInstaller, _ LocalPlan, _ *LocalPlanPublisher) {
			aa28Scribble(spy.installed[0])
		}},
		{"returned copy", func(_ *testing.T, _ *aa28SpyInstaller, returned LocalPlan, _ *LocalPlanPublisher) {
			aa28Scribble(returned)
		}},
		{"current copy", func(t *testing.T, _ *aa28SpyInstaller, _ LocalPlan, publisher *LocalPlanPublisher) {
			current, held := publisher.Current(aa28Address)
			if !held {
				t.Fatal("no current plan")
			}
			aa28Scribble(current)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := aa28NewSpy()
			publisher := aa28NewPublisher(t, spy)
			returned := aa28Publish(t, publisher, aa28Binding())
			tc.scribble(t, spy, returned, publisher)
			current, _ := publisher.Current(aa28Address)
			aa28WantEqual(t, current, aa28WantPlan())
		})
	}
}

func TestAA28CloneSharesNoStorage(t *testing.T) {
	original := aa28WantPlan()
	clone := original.Clone()
	aa28Scribble(clone)
	aa28WantEqual(t, original, aa28WantPlan())
}

// The values are the plan contract's enumerations, stated literally because the
// pinned module does not export them for a direct comparison.
func TestAA28MirroredEnumerationsMatchTheContract(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uint64
	}{
		{"contract revision", uint64(LocalPlanContractRevision), 1},
		{"operation unspecified", uint64(LocalOperationUnspecified), 0},
		{"operation read", uint64(LocalOperationRead), 1},
		{"operation stateful", uint64(LocalOperationStateful), 2},
		{"phase response", uint64(LocalPhaseResponse), 3},
		{"failure unspecified", uint64(LocalFailureUnspecified), 0},
		{"failure silence", uint64(LocalFailureSilence), 1},
		{"failure nack", uint64(LocalFailureNACK), 2},
		{"registration absent", uint64(LocalRegistrationAbsent), 0},
		{"registration active", uint64(LocalRegistrationActive), 1},
		{"registration retiring", uint64(LocalRegistrationRetiring), 2},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// Compiler input and output are plain data: a func, channel or interface field
// would let a runtime callback back into the timing path.
func TestAA28PlanDataCarriesNoRuntimeCallback(t *testing.T) {
	var walk func(t *testing.T, path string, typ reflect.Type, seen map[reflect.Type]bool)
	walk = func(t *testing.T, path string, typ reflect.Type, seen map[reflect.Type]bool) {
		t.Helper()
		if seen[typ] {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer, reflect.Uintptr:
			t.Errorf("%s has kind %s", path, typ.Kind())
		case reflect.Struct:
			for index := 0; index < typ.NumField(); index++ {
				field := typ.Field(index)
				walk(t, path+"."+field.Name, field.Type, seen)
			}
		case reflect.Slice, reflect.Array, reflect.Pointer:
			walk(t, path+"[]", typ.Elem(), seen)
		case reflect.Map:
			walk(t, path+"[key]", typ.Key(), seen)
			walk(t, path+"[value]", typ.Elem(), seen)
		}
	}
	for _, value := range []any{LocalPlanInput{}, LocalPlan{}, LocalProfile{}, LocalPlanBinding{}} {
		typ := reflect.TypeOf(value)
		walk(t, typ.Name(), typ, map[reflect.Type]bool{})
	}
}

func aa28ParseOwned(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "local_plan.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return file
}

// A static check: it catches a new import or legacy reference in this file, and
// says nothing about behaviour reached in some other way.
func TestAA28CompilerStaysOffTheLegacyRuntime(t *testing.T) {
	file := aa28ParseOwned(t)
	allowed := map[string]bool{
		"errors": true,
		"fmt":    true,
		"github.com/Project-Helianthus/helianthus-ebusgo/protocol": true,
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("import %s: %v", spec.Path.Value, err)
		}
		if !allowed[path] {
			t.Errorf("unexpected import %q", path)
		}
	}
	forbidden := map[string]bool{
		"SlaveResponder": true, "NewSlaveResponder": true, "FrameReader": true, "NewFrameReader": true,
		"Run": true, "Emulate": true, "Target": true, "ReadFrame": true, "Write": true, "ReadByte": true,
		"sendResponse": true, "sendByte": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && forbidden[ident.Name] {
			t.Errorf("local_plan.go references %s", ident.Name)
		}
		return true
	})
}

// A static check over this repository's production files: nothing may reach the
// compiler or the publisher, so a default configuration cannot.
func TestAA28DefaultConfigurationDoesNotReachTheCompiler(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	entryPoints := map[string]bool{
		"CompileLocalPlan": true, "NewLocalPlanPublisher": true, "LocalPlanPublisher": true,
		"LocalPlanInstaller": true, "InstallLocalPlan": true,
	}
	scanned := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relative) == "internal/bus/local_plan.go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		scanned++
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && entryPoints[ident.Name] {
				t.Errorf("%s references %s", relative, ident.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production files")
	}
}
