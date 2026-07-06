package balances

import (
	"context"
	"strings"
	"testing"

	"github.com/gnolang/contribs/gnogenesis/internal/common"
	"github.com/gnolang/gno/gno.land/pkg/gnoland"
	"github.com/gnolang/gno/tm2/pkg/bft/types"
	"github.com/gnolang/gno/tm2/pkg/commands"
	"github.com/gnolang/gno/tm2/pkg/std"
	"github.com/gnolang/gno/tm2/pkg/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVestingGenerate_EntryFormat(t *testing.T) {
	t.Parallel()

	const (
		startTime int64 = 1767225600 // 2026-01-01 UTC
		endTime   int64 = 1832985600 // 2028-01-01 UTC
	)

	keys := common.DummyKeys(t, 3)
	addr := []string{
		keys[0].Address().String(),
		keys[1].Address().String(),
		keys[2].Address().String(),
	}

	cases := []struct {
		name    string
		input   allocation
		want    string
		wantErr bool
	}{
		{
			name:  "fully locked continuous",
			input: allocation{Address: addr[0], Total: 1_000_000, Locked: 1_000_000},
			want:  addr[0] + "=1000000ugnot;vesting=1000000ugnot,1767225600,1832985600",
		},
		{
			name:  "partial lock investors-style",
			input: allocation{Address: addr[1], Total: 300_000_000, Locked: 150_000_000},
			want:  addr[1] + "=300000000ugnot;vesting=150000000ugnot,1767225600,1832985600",
		},
		{
			name:  "zero locked emits plain balance",
			input: allocation{Address: addr[2], Total: 500, Locked: 0},
			want:  addr[2] + "=500ugnot",
		},
		{
			name:    "locked exceeds total rejected",
			input:   allocation{Address: addr[0], Total: 100, Locked: 200},
			wantErr: true,
		},
		{
			name:    "invalid address rejected",
			input:   allocation{Address: "not-an-address", Total: 100, Locked: 100},
			wantErr: true,
		},
		{
			name:    "zero total rejected",
			input:   allocation{Address: addr[0], Total: 0, Locked: 0},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := formatVestingEntry(tc.input, startTime, endTime)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestVestingGenerate_ParseSheet(t *testing.T) {
	t.Parallel()

	// Two-column form: "<address> <total> <locked>"
	// Comments (lines starting with #) and blank lines are ignored.
	keys := common.DummyKeys(t, 3)
	sheet := strings.Join([]string{
		"# comment",
		"",
		keys[0].Address().String() + " 1000000 1000000",
		keys[1].Address().String() + " 300000000 150000000",
		keys[2].Address().String() + " 500 0",
	}, "\n")

	allocs, err := parseAllocations(strings.NewReader(sheet))
	require.NoError(t, err)
	require.Len(t, allocs, 3)
	assert.Equal(t, int64(1000000), allocs[0].Total)
	assert.Equal(t, int64(1000000), allocs[0].Locked)
	assert.Equal(t, int64(150000000), allocs[1].Locked) // investors-style: half locked
	assert.Equal(t, int64(0), allocs[2].Locked)
}

func TestVestingGenerate_CommandWritesGenesis(t *testing.T) {
	t.Parallel()

	tempGenesis, cleanup := testutils.NewTestFile(t)
	t.Cleanup(cleanup)

	genesis := common.DefaultGenesis()
	require.NoError(t, genesis.SaveAs(tempGenesis.Name()))

	key := common.DummyKey(t)
	addr := key.Address().String()
	sheetContent := addr + " 1000000 1000000\n"
	sheetFile, sheetCleanup := testutils.NewTestFile(t)
	t.Cleanup(sheetCleanup)
	_, err := sheetFile.WriteString(sheetContent)
	require.NoError(t, err)

	cmd := NewBalancesCmd(commands.NewTestIO())
	args := []string{
		"vesting",
		"--genesis-path", tempGenesis.Name(),
		"--allocations", sheetFile.Name(),
		"--start-time", "1767225600",
		"--end-time", "1832985600",
	}
	require.NoError(t, cmd.ParseAndRun(context.Background(), args))

	// Reload genesis and verify the entry was written with the vesting schedule.
	loaded, err := types.GenesisDocFromFile(tempGenesis.Name())
	require.NoError(t, err)
	state, ok := loaded.AppState.(gnoland.GnoGenesisState)
	require.True(t, ok)
	require.Len(t, state.Balances, 1)

	b := state.Balances[0]
	require.True(t, b.IsVesting(), "expected vesting balance")
	require.Equal(t, std.Coins{std.NewCoin("ugnot", 1000000)}, b.Amount)
	require.Equal(t, addr, b.Address.String())
	require.Equal(t, int64(1767225600), b.Vesting.StartTime)
	require.Equal(t, int64(1832985600), b.Vesting.EndTime)
	require.Equal(t, std.VestingContinuous, b.Vesting.Type)
}

func TestVestingGenerate_RejectsBadSchedule(t *testing.T) {
	t.Parallel()

	tempGenesis, cleanup := testutils.NewTestFile(t)
	t.Cleanup(cleanup)
	genesis := common.DefaultGenesis()
	require.NoError(t, genesis.SaveAs(tempGenesis.Name()))

	sheetFile, sheetCleanup := testutils.NewTestFile(t)
	t.Cleanup(sheetCleanup)
	key := common.DummyKey(t)
	_, err := sheetFile.WriteString(key.Address().String() + " 1000000 1000000\n")
	require.NoError(t, err)

	cmd := NewBalancesCmd(commands.NewTestIO())
	args := []string{
		"vesting",
		"--genesis-path", tempGenesis.Name(),
		"--allocations", sheetFile.Name(),
		"--start-time", "1832985600", // start after end
		"--end-time", "1767225600",
	}
	err = cmd.ParseAndRun(context.Background(), args)
	require.Error(t, err)
}
