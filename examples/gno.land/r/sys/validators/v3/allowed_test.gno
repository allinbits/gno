package validators

import (
	"testing"

	"gno.land/p/nt/avl/v0"
	"gno.land/p/nt/uassert/v0"
	"gno.land/p/nt/urequire/v0"
	"gno.land/p/sys/validators"
)

func TestAllowedRealms_AddRemove(t *testing.T) {
	allowedRealms = avl.NewTree()

	uassert.False(t, IsAllowedRealm("gno.land/r/demo/ics"))

	allowedRealms.Set("gno.land/r/demo/ics", true)
	uassert.True(t, IsAllowedRealm("gno.land/r/demo/ics"))

	realms := GetAllowedRealms()
	uassert.Equal(t, 1, len(realms))
	uassert.Equal(t, "gno.land/r/demo/ics", realms[0])

	allowedRealms.Remove("gno.land/r/demo/ics")
	uassert.False(t, IsAllowedRealm("gno.land/r/demo/ics"))
	uassert.Equal(t, 0, len(GetAllowedRealms()))
}

func TestAddValidator_NotAllowed(cur realm, t *testing.T) {
	allowedRealms = avl.NewTree()
	resetValset(t)

	testing.SetRealm(testing.NewCodeRealm("gno.land/r/demo/unknown"))

	val := validators.Validator{
		Address:     mustAddr(t, pubKeyA),
		PubKey:      pubKeyA,
		VotingPower: 10,
	}

	urequire.AbortsWithMessage(t,
		"realm gno.land/r/demo/unknown is not allowed to modify the validator set",
		func() { AddValidator(cross, val) },
	)
}

func TestAddValidator_UserNotAllowed(cur realm, t *testing.T) {
	allowedRealms = avl.NewTree()
	resetValset(t)

	testing.SetRealm(testing.NewUserRealm(mustAddr(t, pubKeyA)))

	val := validators.Validator{
		Address:     mustAddr(t, pubKeyA),
		PubKey:      pubKeyA,
		VotingPower: 10,
	}

	urequire.AbortsWithMessage(t,
		"caller must be a realm, not a user",
		func() { AddValidator(cross, val) },
	)
}

func TestAddValidator_Allowed(cur realm, t *testing.T) {
	allowedRealms = avl.NewTree()
	resetValset(t)
	testing.SetSysParamStrings(module, submodule, currKey, []string{pubKeyA + ":10"})

	const allowedPath = "gno.land/r/demo/ics"
	allowedRealms.Set(allowedPath, true)

	testing.SetRealm(testing.NewCodeRealm(allowedPath))

	addrB := mustAddr(t, pubKeyB)
	val := validators.Validator{
		Address:     addrB,
		PubKey:      pubKeyB,
		VotingPower: 10,
	}

	AddValidator(cross, val)
	uassert.True(t, IsValidator(addrB))
}

func TestRemoveValidator_Allowed(cur realm, t *testing.T) {
	allowedRealms = avl.NewTree()
	resetValset(t)
	testing.SetSysParamStrings(module, submodule, currKey, []string{pubKeyA + ":10"})

	const allowedPath = "gno.land/r/demo/ics"
	allowedRealms.Set(allowedPath, true)

	testing.SetRealm(testing.NewCodeRealm(allowedPath))

	addrB := mustAddr(t, pubKeyB)
	val := validators.Validator{
		Address:     addrB,
		PubKey:      pubKeyB,
		VotingPower: 10,
	}

	AddValidator(cross, val)
	uassert.True(t, IsValidator(addrB))

	RemoveValidator(cross, addrB)
	uassert.False(t, IsValidator(addrB))
}

func TestRemoveValidator_NotAllowed(cur realm, t *testing.T) {
	allowedRealms = avl.NewTree()
	resetValset(t)

	testing.SetRealm(testing.NewCodeRealm("gno.land/r/demo/unknown"))

	urequire.AbortsWithMessage(t,
		"realm gno.land/r/demo/unknown is not allowed to modify the validator set",
		func() { RemoveValidator(cross, mustAddr(t, pubKeyA)) },
	)
}
