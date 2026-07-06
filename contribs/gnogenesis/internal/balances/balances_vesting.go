package balances

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/gnolang/gno/gno.land/pkg/gnoland"
	"github.com/gnolang/gno/gno.land/pkg/gnoland/ugnot"
	"github.com/gnolang/gno/tm2/pkg/bft/types"
	"github.com/gnolang/gno/tm2/pkg/commands"
	"github.com/gnolang/gno/tm2/pkg/crypto"
)

var (
	errNoAllocationsSource = errors.New("allocations file must be set")
	errInvalidSchedule     = errors.New("invalid vesting schedule")
)

const allocationsFormat = "<address> <total> <locked>  (whitespace-separated; comments with #)"

type balancesVestingCfg struct {
	rootCfg *balancesCfg

	allocations string
	startTime   int64
	endTime     int64
	delayed     bool
}

// newBalancesVestingCmd creates the genesis balances vesting subcommand.
//
// It reads a simple allocation sheet (one recipient per line) and writes
// vesting balance entries into the genesis.json. Each line is:
//
//	<address> <total> <locked>
//
// where <total> is the full allocation in ugnot and <locked> is the portion
// subject to the vesting schedule (0 means no vesting, just a plain balance).
// Lines starting with '#' and blank lines are ignored.
func newBalancesVestingCmd(rootCfg *balancesCfg, io commands.IO) *commands.Command {
	cfg := &balancesVestingCfg{rootCfg: rootCfg}

	return commands.NewCommand(
		commands.Metadata{
			Name:       "vesting",
			ShortUsage: "balances vesting [flags]",
			ShortHelp:  "generates vesting balance entries from an allocation sheet",
			LongHelp: "Reads an allocation sheet and writes linear-vesting balance entries " +
				"into the genesis.json. Each line of the sheet is " +
				allocationsFormat + ".",
		},
		cfg,
		func(ctx context.Context, _ []string) error {
			return execBalancesVesting(ctx, cfg, io)
		},
	)
}

func (c *balancesVestingCfg) RegisterFlags(fs *flag.FlagSet) {
	fs.StringVar(
		&c.allocations,
		"allocations",
		"",
		"path to the allocation sheet, one entry per line: "+allocationsFormat,
	)
	fs.Int64Var(
		&c.startTime,
		"start-time",
		0,
		"vesting start time as a Unix timestamp (seconds)",
	)
	fs.Int64Var(
		&c.endTime,
		"end-time",
		0,
		"vesting end time as a Unix timestamp (seconds)",
	)
	fs.BoolVar(
		&c.delayed,
		"delayed",
		false,
		"use delayed (cliff) vesting instead of the default linear schedule",
	)
}

func execBalancesVesting(_ context.Context, cfg *balancesVestingCfg, io commands.IO) error {
	if cfg.allocations == "" {
		return errNoAllocationsSource
	}
	if cfg.endTime <= 0 {
		return fmt.Errorf("%w: --end-time must be set", errInvalidSchedule)
	}
	if cfg.startTime >= cfg.endTime {
		return fmt.Errorf(
			"%w: start-time (%d) must be before end-time (%d)",
			errInvalidSchedule, cfg.startTime, cfg.endTime,
		)
	}

	file, err := os.Open(cfg.allocations)
	if err != nil {
		return fmt.Errorf("unable to open allocations sheet: %w", err)
	}
	defer file.Close()

	allocs, err := parseAllocations(file)
	if err != nil {
		return fmt.Errorf("unable to parse allocations: %w", err)
	}

	entries := make([]string, 0, len(allocs))
	for _, a := range allocs {
		entry, err := formatVestingEntry(a, cfg.startTime, cfg.endTime)
		if err != nil {
			return fmt.Errorf("unable to format entry for %s: %w", a.Address, err)
		}
		if cfg.delayed && a.Locked > 0 {
			entry += ";type=delayed"
		}
		entries = append(entries, entry)
	}

	balances, err := gnoland.GetBalancesFromEntries(entries...)
	if err != nil {
		return fmt.Errorf("unable to build balances: %w", err)
	}

	// Load genesis, merge, and save — same pattern as `balances add`.
	genesis, loadErr := types.GenesisDocFromFile(cfg.rootCfg.GenesisPath)
	if loadErr != nil {
		return fmt.Errorf("unable to load genesis: %w", loadErr)
	}

	if genesis.AppState == nil {
		genesis.AppState = gnoland.GnoGenesisState{}
	}
	state := genesis.AppState.(gnoland.GnoGenesisState)

	genesisBalances, err := mapGenesisBalancesFromState(state)
	if err != nil {
		return err
	}
	// Input takes precedence over any pre-existing entries.
	balances.LeftMerge(genesisBalances)

	sorted := balances.List()
	state.Balances = sorted
	genesis.AppState = state

	if err := genesis.SaveAs(cfg.rootCfg.GenesisPath); err != nil {
		return fmt.Errorf("unable to save genesis.json: %w", err)
	}

	for _, b := range sorted {
		io.Printfln("%s", b.String())
	}
	io.Println()
	io.Printfln("%d balances saved", len(sorted))

	return nil
}

// allocation is a single recipient row from the allocation sheet.
type allocation struct {
	Address string
	Total   int64 // ugnot
	Locked  int64 // ugnot; 0 means no vesting
}

// parseAllocations reads the allocation sheet.
// Format per line: "<address> <total> <locked>".
// Locked may be omitted (defaults to 0). Comments (#) and blank lines are skipped.
func parseAllocations(r io.Reader) ([]allocation, error) {
	var allocs []allocation

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()

		// Strip comments and trim.
		if i := strings.Index(raw, "#"); i >= 0 {
			raw = raw[:i]
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		fields := strings.Fields(raw)
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf(
				"line %d: expected %s, got %q",
				lineNo, allocationsFormat, raw,
			)
		}

		a := allocation{Address: fields[0]}

		total, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid total %q: %w", lineNo, fields[1], err)
		}
		a.Total = total

		if len(fields) == 3 {
			locked, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid locked %q: %w", lineNo, fields[2], err)
			}
			a.Locked = locked
		}

		allocs = append(allocs, a)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading allocations: %w", err)
	}
	return allocs, nil
}

// formatVestingEntry builds a single balance-sheet entry string.
// When locked == 0 the entry has no vesting suffix (plain balance).
func formatVestingEntry(a allocation, startTime, endTime int64) (string, error) {
	if _, err := crypto.AddressFromBech32(a.Address); err != nil {
		return "", fmt.Errorf("invalid address %q: %w", a.Address, err)
	}
	if a.Total <= 0 {
		return "", fmt.Errorf("total must be positive, got %d", a.Total)
	}
	if a.Locked < 0 || a.Locked > a.Total {
		return "", fmt.Errorf("locked (%d) must be in [0, total (%d)]", a.Locked, a.Total)
	}

	if a.Locked == 0 {
		return fmt.Sprintf("%s=%s", a.Address, ugnot.ValueString(a.Total)), nil
	}

	return fmt.Sprintf(
		"%s=%s;vesting=%s,%d,%d",
		a.Address,
		ugnot.ValueString(a.Total),
		ugnot.ValueString(a.Locked),
		startTime,
		endTime,
	), nil
}
