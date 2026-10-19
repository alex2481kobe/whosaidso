package model

// SetAsideResolve records that someone dealt with a set-aside bundle: they
// re-recorded its change, stopped a lost claim, or decided it stays out. It
// names the bundle and why. It is never refused for naming a bundle that is
// not set aside, or one already resolved: both are no-ops (reduce/
// setaside_resolve.go), so two resolutions made on two branches never clash.
type SetAsideResolve struct {
	Bundle BundleRef `json:"bundle"`
	Reason string    `json:"reason" semantic:"text"`
}

func (*SetAsideResolve) eventPayload()        {}
func (*SetAsideResolve) EventType() EventType { return "setaside.resolve" }
