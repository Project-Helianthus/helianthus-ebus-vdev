package bus

import (
	"errors"
	"fmt"

	"github.com/Project-Helianthus/helianthus-ebusgo/protocol"
)

// This file compiles a finite read-only virtual-device fixture profile into the
// prepared responder-plan shape of the local worker registration and plan
// contract. It is data to data: it opens no transport, starts no responder and
// calls no Target.Emulate, GraphQL fetch or semantic query. Nothing in a default
// configuration invokes it, and it adds no arming switch.
//
// The contract types are mirrored here because the pinned ebusgo module does not
// export them yet. An adapter behind LocalPlanInstaller owns that mapping and
// states the revision it implements; any other revision stops compilation.

const (
	// LocalPlanContractRevision is the one contract revision this compiler targets.
	LocalPlanContractRevision uint32 = 1

	localMaxNativeLength    = 255
	localMaxEncodedResponse = 2 * (1 + localMaxNativeLength + 1)
	localMaxProfileIDBytes  = 64
)

// Refusals are sentinels. A refusal returns no plan and installs nothing.
var (
	ErrLocalPlanContractMismatch    = errors.New("local plan: installer contract mismatch")
	ErrLocalPlanInvalidProfile      = errors.New("local plan: malformed profile")
	ErrLocalPlanInvalidIdentity     = errors.New("local plan: zero identity")
	ErrLocalPlanInvalidAddress      = errors.New("local plan: address not admitted")
	ErrLocalPlanNotReadOnly         = errors.New("local plan: entry is not an explicit read")
	ErrLocalPlanUnsupportedFailure  = errors.New("local plan: unsupported failure policy")
	ErrLocalPlanUnknownRegistration = errors.New("local plan: no registration for this exact key")
	ErrLocalPlanConflict            = errors.New("local plan: conflicting registration")
	ErrLocalPlanSourceMismatch      = errors.New("local plan: source generation differs from the registration")
	ErrLocalPlanOversize            = errors.New("local plan: exceeds a contract bound")
	ErrLocalPlanAmbiguous           = errors.New("local plan: entries overlap")
	ErrLocalPlanClock               = errors.New("local plan: clock reading not comparable")
	ErrLocalPlanExpired             = errors.New("local plan: lease or plan expired")
	ErrLocalPlanLeaseTooLong        = errors.New("local plan: plan lease exceeds the contract bound")
	ErrLocalPlanStaleGeneration     = errors.New("local plan: generation does not advance")
	ErrLocalPlanNoInstaller         = errors.New("local plan: no installer")
)

// LocalOperation classifies an entry. Only reads compile.
type LocalOperation uint8

const (
	LocalOperationUnspecified LocalOperation = iota
	LocalOperationRead
	LocalOperationStateful
)

// LocalPhase names the native transaction phase an entry may answer in.
type LocalPhase uint8

// LocalPhaseResponse is the only phase a compiled entry may answer in.
const LocalPhaseResponse LocalPhase = 3

// LocalFailureAction states what the responder does in a failure class.
type LocalFailureAction uint8

const (
	LocalFailureUnspecified LocalFailureAction = iota
	LocalFailureSilence
	LocalFailureNACK
)

// LocalFailurePolicy must name every failure class. Only silence is admitted:
// refusal is not a universal NACK rule.
type LocalFailurePolicy struct {
	Unavailable LocalFailureAction
	Malformed   LocalFailureAction
	Timeout     LocalFailureAction
}

func (policy LocalFailurePolicy) validate() error {
	for _, action := range [...]LocalFailureAction{policy.Unavailable, policy.Malformed, policy.Timeout} {
		if action != LocalFailureSilence {
			return ErrLocalPlanUnsupportedFailure
		}
	}
	return nil
}

// LocalSourceSelector restricts the initiator of a request. Any and an exact
// source are mutually exclusive.
type LocalSourceSelector struct {
	Any    bool
	Source byte
}

// LocalProfileEntry is one explicit request selector and its prepared reply.
type LocalProfileEntry struct {
	Source          LocalSourceSelector
	PB              byte
	SB              byte
	RequestLength   uint32
	Request         []byte
	Operation       LocalOperation
	MaxEncodedBytes uint32
	Reply           []byte
}

// LocalProfile is a minimal read-only virtual-device fixture.
type LocalProfile struct {
	ID      string
	Address byte
	Failure LocalFailurePolicy
	Entries []LocalProfileEntry
}

// LocalRegistrationKey compares as one complete tuple, never by address alone.
type LocalRegistrationKey struct {
	Subscriber uint64
	Address    byte
	Epoch      uint64
}

// LocalPlanBinding names the generations one compilation is made for.
type LocalPlanBinding struct {
	Subscriber uint64
	Epoch      uint64
	Worker     uint64
	Stream     uint64
	Plan       uint64
	Source     uint64
	PlanExpiry uint64
}

func (binding LocalPlanBinding) key(address byte) LocalRegistrationKey {
	return LocalRegistrationKey{Subscriber: binding.Subscriber, Address: address, Epoch: binding.Epoch}
}

// LocalRegistrationState is the lifecycle of one registration key.
type LocalRegistrationState uint8

const (
	LocalRegistrationAbsent LocalRegistrationState = iota
	LocalRegistrationActive
	LocalRegistrationRetiring
)

// LocalRegistrationView is a detached snapshot of one registration.
type LocalRegistrationView struct {
	Key               LocalRegistrationKey
	State             LocalRegistrationState
	LeaseExpiry       uint64
	Source            uint64
	SourceLeaseExpiry uint64
}

// LocalClockReading is one injected monotonic reading. Domain zero is not
// comparable.
type LocalClockReading struct {
	Domain uint64
	Tick   uint64
}

// LocalPlanContract is what the installer declares it can accept.
type LocalPlanContract struct {
	Revision                uint32
	MaxEntries              uint32
	MaxRequestBytes         uint32
	MaxEncodedResponseBytes uint32
	MaxPlanBytes            uint32
	MaxLeaseTicks           uint64
}

func (contract LocalPlanContract) validate() error {
	if contract.Revision != LocalPlanContractRevision {
		return ErrLocalPlanContractMismatch
	}
	if contract.MaxEntries == 0 || contract.MaxRequestBytes == 0 || contract.MaxEncodedResponseBytes == 0 ||
		contract.MaxLeaseTicks == 0 {
		return ErrLocalPlanContractMismatch
	}
	if contract.MaxRequestBytes > localMaxNativeLength || contract.MaxEncodedResponseBytes > localMaxEncodedResponse {
		return ErrLocalPlanContractMismatch
	}
	if uint64(contract.MaxPlanBytes) < uint64(contract.MaxRequestBytes)+uint64(contract.MaxEncodedResponseBytes) {
		return ErrLocalPlanContractMismatch
	}
	return nil
}

// LocalPlanIdentity is the complete identity of one compiled plan.
type LocalPlanIdentity struct {
	Registration LocalRegistrationKey
	Worker       uint64
	Stream       uint64
	Plan         uint64
	Source       uint64
}

// LocalPlanValidity holds the exclusive deadlines frozen into a plan. The
// registration and source leases come from the registration, never the profile.
type LocalPlanValidity struct {
	Domain             uint64
	PlanExpiry         uint64
	RegistrationExpiry uint64
	SourceLeaseExpiry  uint64
}

// LocalPlanEntry is one exact match and its prepared wire response: LEN DATA CRC,
// escaped, as the slave FSM sends it after the target ACK.
type LocalPlanEntry struct {
	Source          LocalSourceSelector
	Destination     byte
	PB              byte
	SB              byte
	RequestLength   uint32
	Request         []byte
	Operation       LocalOperation
	Phase           LocalPhase
	MaxEncodedBytes uint32
	EncodedResponse []byte
}

// LocalPlan is one complete compiled generation. It owns its byte slices.
type LocalPlan struct {
	ProfileID string
	Identity  LocalPlanIdentity
	Validity  LocalPlanValidity
	Failure   LocalFailurePolicy
	Entries   []LocalPlanEntry
}

// Clone returns a plan that shares no mutable storage with the receiver.
func (plan LocalPlan) Clone() LocalPlan {
	clone := plan
	clone.Entries = make([]LocalPlanEntry, len(plan.Entries))
	for index, entry := range plan.Entries {
		entry.Request = append([]byte(nil), entry.Request...)
		entry.EncodedResponse = append([]byte(nil), entry.EncodedResponse...)
		clone.Entries[index] = entry
	}
	return clone
}

// LocalPlanInput is everything one compilation may read.
type LocalPlanInput struct {
	Profile         LocalProfile
	Binding         LocalPlanBinding
	Contract        LocalPlanContract
	Registration    LocalRegistrationView
	AddressConflict bool
	Now             LocalClockReading
}

// CompileLocalPlan validates the whole input, then builds a detached plan. It has
// no effect and reads nothing but its argument.
func CompileLocalPlan(input LocalPlanInput) (LocalPlan, error) {
	if err := input.Contract.validate(); err != nil {
		return LocalPlan{}, err
	}
	profile := input.Profile
	binding := input.Binding
	if profile.ID == "" || len(profile.ID) > localMaxProfileIDBytes {
		return LocalPlan{}, ErrLocalPlanInvalidProfile
	}
	if binding.Subscriber == 0 || binding.Epoch == 0 || binding.Worker == 0 || binding.Stream == 0 ||
		binding.Plan == 0 || binding.Source == 0 {
		return LocalPlan{}, ErrLocalPlanInvalidIdentity
	}
	if !isLocalTargetAddress(profile.Address) {
		return LocalPlan{}, ErrLocalPlanInvalidAddress
	}
	if input.AddressConflict {
		return LocalPlan{}, ErrLocalPlanConflict
	}
	key := binding.key(profile.Address)
	validity, err := checkLocalRegistration(input, key)
	if err != nil {
		return LocalPlan{}, err
	}
	if err := profile.Failure.validate(); err != nil {
		return LocalPlan{}, err
	}
	entries, err := compileLocalEntries(profile, input.Contract)
	if err != nil {
		return LocalPlan{}, err
	}
	return LocalPlan{
		ProfileID: profile.ID,
		Identity: LocalPlanIdentity{
			Registration: key,
			Worker:       binding.Worker,
			Stream:       binding.Stream,
			Plan:         binding.Plan,
			Source:       binding.Source,
		},
		Validity: validity,
		Failure:  profile.Failure,
		Entries:  entries,
	}, nil
}

// isLocalTargetAddress admits an initiator/target destination: no broadcast,
// initiator pattern, escape or SYN.
func isLocalTargetAddress(address byte) bool {
	switch address {
	case protocol.AddressBroadcast, protocol.SymbolEscape, protocol.SymbolSyn:
		return false
	}
	return !protocol.IsInitiatorCapableAddress(address)
}

// checkLocalRegistration binds the plan to one active registration and admits
// only strictly before each deadline: equality is expired.
func checkLocalRegistration(input LocalPlanInput, key LocalRegistrationKey) (LocalPlanValidity, error) {
	view := input.Registration
	switch view.State {
	case LocalRegistrationActive:
	case LocalRegistrationRetiring:
		return LocalPlanValidity{}, ErrLocalPlanConflict
	default:
		return LocalPlanValidity{}, ErrLocalPlanUnknownRegistration
	}
	if view.Key != key {
		return LocalPlanValidity{}, ErrLocalPlanConflict
	}
	if view.Source != input.Binding.Source {
		return LocalPlanValidity{}, ErrLocalPlanSourceMismatch
	}
	now := input.Now
	if now.Domain == 0 {
		return LocalPlanValidity{}, ErrLocalPlanClock
	}
	if now.Tick >= input.Binding.PlanExpiry || now.Tick >= view.LeaseExpiry || now.Tick >= view.SourceLeaseExpiry {
		return LocalPlanValidity{}, ErrLocalPlanExpired
	}
	if input.Binding.PlanExpiry-now.Tick > input.Contract.MaxLeaseTicks {
		return LocalPlanValidity{}, ErrLocalPlanLeaseTooLong
	}
	return LocalPlanValidity{
		Domain:             now.Domain,
		PlanExpiry:         input.Binding.PlanExpiry,
		RegistrationExpiry: view.LeaseExpiry,
		SourceLeaseExpiry:  view.SourceLeaseExpiry,
	}, nil
}

type localRequestKey struct {
	pb      byte
	sb      byte
	request string
}

func compileLocalEntries(profile LocalProfile, contract LocalPlanContract) ([]LocalPlanEntry, error) {
	if len(profile.Entries) == 0 {
		return nil, ErrLocalPlanInvalidProfile
	}
	if uint64(len(profile.Entries)) > uint64(contract.MaxEntries) {
		return nil, ErrLocalPlanOversize
	}
	entries := make([]LocalPlanEntry, len(profile.Entries))
	selectors := make(map[localRequestKey][]LocalSourceSelector, len(profile.Entries))
	total := uint64(0)
	for index, source := range profile.Entries {
		entry, err := compileLocalEntry(source, profile.Address, contract)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", index, err)
		}
		requestKey := localRequestKey{pb: source.PB, sb: source.SB, request: string(source.Request)}
		// Overlap stops the scan, so a key holds at most one selector per source.
		for _, prior := range selectors[requestKey] {
			if prior.Any || source.Source.Any || prior.Source == source.Source.Source {
				return nil, fmt.Errorf("entry %d: %w", index, ErrLocalPlanAmbiguous)
			}
		}
		selectors[requestKey] = append(selectors[requestKey], source.Source)
		total += uint64(len(entry.Request)) + uint64(len(entry.EncodedResponse))
		if total > uint64(contract.MaxPlanBytes) {
			return nil, ErrLocalPlanOversize
		}
		entries[index] = entry
	}
	return entries, nil
}

func compileLocalEntry(source LocalProfileEntry, address byte, contract LocalPlanContract) (LocalPlanEntry, error) {
	if source.Operation != LocalOperationRead {
		return LocalPlanEntry{}, ErrLocalPlanNotReadOnly
	}
	if source.Source.Any {
		if source.Source.Source != 0 {
			return LocalPlanEntry{}, ErrLocalPlanInvalidProfile
		}
	} else if !protocol.IsInitiatorCapableAddress(source.Source.Source) {
		return LocalPlanEntry{}, ErrLocalPlanInvalidProfile
	}
	if uint64(source.RequestLength) != uint64(len(source.Request)) {
		return LocalPlanEntry{}, ErrLocalPlanInvalidProfile
	}
	if uint64(len(source.Request)) > uint64(contract.MaxRequestBytes) {
		return LocalPlanEntry{}, ErrLocalPlanOversize
	}
	if source.MaxEncodedBytes == 0 {
		return LocalPlanEntry{}, ErrLocalPlanInvalidProfile
	}
	if source.MaxEncodedBytes > contract.MaxEncodedResponseBytes || len(source.Reply) > localMaxNativeLength {
		return LocalPlanEntry{}, ErrLocalPlanOversize
	}
	encoded, err := protocol.EncodeSlaveResponse(source.Reply)
	if err != nil {
		return LocalPlanEntry{}, fmt.Errorf("%w: %v", ErrLocalPlanInvalidProfile, err)
	}
	if uint64(len(encoded)) > uint64(source.MaxEncodedBytes) {
		return LocalPlanEntry{}, ErrLocalPlanOversize
	}
	return LocalPlanEntry{
		Source:          source.Source,
		Destination:     address,
		PB:              source.PB,
		SB:              source.SB,
		RequestLength:   source.RequestLength,
		Request:         append([]byte(nil), source.Request...),
		Operation:       LocalOperationRead,
		Phase:           LocalPhaseResponse,
		MaxEncodedBytes: source.MaxEncodedBytes,
		EncodedResponse: encoded,
	}, nil
}

// LocalPlanInstaller is the injected consumer of compiled plans. The registry
// behind it stays the authority on registration, plan generation and pool state;
// the two reads are exact and detached. InstallLocalPlan takes a whole generation
// or refuses it.
type LocalPlanInstaller interface {
	LocalPlanContract() LocalPlanContract
	LocalPlanRegistration(key LocalRegistrationKey) LocalRegistrationView
	LocalPlanAddressConflict(address byte) bool
	InstallLocalPlan(plan LocalPlan) error
}

// LocalPlanPublisher keeps the last installed generation per address. A refused
// compilation or installation leaves it intact. As in the plan contract, one
// caller drives a publisher at a time.
type LocalPlanPublisher struct {
	installer LocalPlanInstaller
	current   map[byte]LocalPlan
}

// NewLocalPlanPublisher binds a publisher to its installer.
func NewLocalPlanPublisher(installer LocalPlanInstaller) (*LocalPlanPublisher, error) {
	if installer == nil {
		return nil, ErrLocalPlanNoInstaller
	}
	return &LocalPlanPublisher{installer: installer, current: make(map[byte]LocalPlan)}, nil
}

// Publish compiles one generation and hands the installer its own copy. The
// installer's contract is checked before any other read. Deadlines are frozen
// into the plan: enforcing them after installation is the worker's job.
func (publisher *LocalPlanPublisher) Publish(profile LocalProfile, binding LocalPlanBinding, now LocalClockReading) (LocalPlan, error) {
	contract := publisher.installer.LocalPlanContract()
	if err := contract.validate(); err != nil {
		return LocalPlan{}, err
	}
	key := binding.key(profile.Address)
	plan, err := CompileLocalPlan(LocalPlanInput{
		Profile:         profile,
		Binding:         binding,
		Contract:        contract,
		Registration:    publisher.installer.LocalPlanRegistration(key),
		AddressConflict: publisher.installer.LocalPlanAddressConflict(profile.Address),
		Now:             now,
	})
	if err != nil {
		return LocalPlan{}, err
	}
	if previous, held := publisher.current[profile.Address]; held {
		if err := publisher.checkSuccessor(previous, plan); err != nil {
			return LocalPlan{}, err
		}
	}
	if err := publisher.installer.InstallLocalPlan(plan.Clone()); err != nil {
		return LocalPlan{}, fmt.Errorf("install local plan: %w", err)
	}
	publisher.current[profile.Address] = plan
	return plan.Clone(), nil
}

// checkSuccessor refuses a generation that cannot follow the current one. Plan
// generations advance within a worker across its stream changes, and a new worker
// restarts them. A different key may take the address only after the registry has
// released the previous key.
func (publisher *LocalPlanPublisher) checkSuccessor(previous LocalPlan, next LocalPlan) error {
	before := previous.Identity
	after := next.Identity
	if after.Worker < before.Worker {
		return ErrLocalPlanStaleGeneration
	}
	if after.Worker == before.Worker && (after.Stream < before.Stream || after.Plan <= before.Plan) {
		return ErrLocalPlanStaleGeneration
	}
	if after.Registration == before.Registration {
		if after.Source < before.Source {
			return ErrLocalPlanStaleGeneration
		}
		return nil
	}
	if publisher.installer.LocalPlanRegistration(before.Registration).State != LocalRegistrationAbsent {
		return ErrLocalPlanConflict
	}
	return nil
}

// Current returns a detached copy of the last installed generation for an address.
func (publisher *LocalPlanPublisher) Current(address byte) (LocalPlan, bool) {
	plan, held := publisher.current[address]
	if !held {
		return LocalPlan{}, false
	}
	return plan.Clone(), true
}
