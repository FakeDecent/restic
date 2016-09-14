package main

import (
	"fmt"
	"io"
	"restic"
	"strings"
)

// CmdForget implements the 'forget' command.
type CmdForget struct {
	Last    int `short:"l" long:"keep-last"   description:"keep the last n snapshots"`
	Hourly  int `short:"H" long:"keep-hourly" description:"keep the last n hourly snapshots"`
	Daily   int `short:"d" long:"keep-daily"  description:"keep the last n daily snapshots"`
	Weekly  int `short:"w" long:"keep-weekly" description:"keep the last n weekly snapshots"`
	Monthly int `short:"m" long:"keep-monthly"description:"keep the last n monthly snapshots"`
	Yearly  int `short:"y" long:"keep-yearly" description:"keep the last n yearly snapshots"`

	KeepTags []string `long:"keep-tag"    description:"alwaps keep snapshots with this tag (can be specified multiple times)"`

	Hostname string   `long:"hostname" description:"only forget snapshots for the given hostname"`
	Tags     []string `long:"tag"      description:"only forget snapshots with the tag (can be specified multiple times)"`

	DryRun bool `short:"n" long:"dry-run" description:"do not delete anything, just print what would be done"`

	global *GlobalOptions
}

func init() {
	_, err := parser.AddCommand("forget",
		"removes snapshots from a repository",
		`
The forget command removes snapshots according to a policy. Please note
that this command really only deletes the snapshot object in the repo, which
is a reference to data stored there. In order to remove this (now
unreferenced) data after 'forget' was run successfully, see the 'prune'
command.
`,
		&CmdForget{global: &globalOpts})
	if err != nil {
		panic(err)
	}
}

// Usage returns usage information for 'forget'.
func (cmd CmdForget) Usage() string {
	return "[snapshot ID] ..."
}

func printSnapshots(w io.Writer, snapshots restic.Snapshots) {
	tab := NewTable()
	tab.Header = fmt.Sprintf("%-8s  %-19s  %-10s  %-10s  %s", "ID", "Date", "Host", "Tags", "Directory")
	tab.RowFormat = "%-8s  %-19s  %-10s  %-10s  %s"

	for _, sn := range snapshots {
		if len(sn.Paths) == 0 {
			continue
		}

		firstTag := ""
		if len(sn.Tags) > 0 {
			firstTag = sn.Tags[0]
		}

		tab.Rows = append(tab.Rows, []interface{}{sn.ID().Str(), sn.Time.Format(TimeFormat), sn.Hostname, firstTag, sn.Paths[0]})

		rows := len(sn.Paths)
		if len(sn.Tags) > rows {
			rows = len(sn.Tags)
		}

		for i := 1; i < rows; i++ {
			path := ""
			if len(sn.Paths) > i {
				path = sn.Paths[i]
			}

			tag := ""
			if len(sn.Tags) > i {
				tag = sn.Tags[i]
			}

			tab.Rows = append(tab.Rows, []interface{}{"", "", "", tag, path})
		}
	}

	tab.Write(w)
}

// Execute runs the 'forget' command.
func (cmd CmdForget) Execute(args []string) error {
	repo, err := cmd.global.OpenRepository()
	if err != nil {
		return err
	}

	lock, err := lockRepoExclusive(repo)
	defer unlockRepo(lock)
	if err != nil {
		return err
	}

	err = repo.LoadIndex()
	if err != nil {
		return err
	}

	// first, process all snapshot IDs given as arguments
	for _, s := range args {
		id, err := restic.FindSnapshot(repo, s)
		if err != nil {
			return err
		}

		if !cmd.DryRun {
			err = repo.Backend().Remove(restic.SnapshotFile, id.String())
			if err != nil {
				return err
			}

			cmd.global.Verbosef("removed snapshot %v\n", id.Str())
		} else {
			cmd.global.Verbosef("would removed snapshot %v\n", id.Str())
		}
	}

	policy := restic.ExpirePolicy{
		Last:    cmd.Last,
		Hourly:  cmd.Hourly,
		Daily:   cmd.Daily,
		Weekly:  cmd.Weekly,
		Monthly: cmd.Monthly,
		Yearly:  cmd.Yearly,
		Tags:    cmd.KeepTags,
	}

	if policy.Empty() {
		return nil
	}

	// then, load all remaining snapshots
	snapshots, err := restic.LoadAllSnapshots(repo)
	if err != nil {
		return err
	}

	// group by hostname and dirs
	type key struct {
		Hostname string
		Dirs     string
	}

	snapshotGroups := make(map[key]restic.Snapshots)

	for _, sn := range snapshots {
		if cmd.Hostname != "" && sn.Hostname != cmd.Hostname {
			continue
		}

		if !sn.HasTags(cmd.Tags) {
			continue
		}

		k := key{Hostname: sn.Hostname, Dirs: strings.Join(sn.Paths, ":")}
		list := snapshotGroups[k]
		list = append(list, sn)
		snapshotGroups[k] = list
	}

	for key, snapshotGroup := range snapshotGroups {
		cmd.global.Printf("snapshots for host %v, directories %v:\n\n", key.Hostname, key.Dirs)
		keep, remove := restic.ApplyPolicy(snapshotGroup, policy)

		cmd.global.Printf("keep %d snapshots:\n", len(keep))
		printSnapshots(cmd.global.stdout, keep)
		cmd.global.Printf("\n")

		cmd.global.Printf("remove %d snapshots:\n", len(remove))
		printSnapshots(cmd.global.stdout, remove)
		cmd.global.Printf("\n")

		if !cmd.DryRun {
			for _, sn := range remove {
				err = repo.Backend().Remove(restic.SnapshotFile, sn.ID().String())
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}