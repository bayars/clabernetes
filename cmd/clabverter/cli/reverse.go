package cli

import (
	clabernetesclabverter "github.com/srl-labs/clabernetes/clabverter"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	"github.com/urfave/cli/v2"
)

const (
	reverseInputDirectory  = "input-directory"
	reverseOutputDirectory = "output-directory"
	reverseFromSnapshot    = "from-snapshot"
)

func reverseCommand() *cli.Command {
	return &cli.Command{
		Name:  "reverse",
		Usage: "convert a clabverter output directory (or snapshot ConfigMap) back to a containerlab topology file and device config files",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     reverseInputDirectory,
				Usage:    "directory containing clabverter output (Topology CR + ConfigMaps)",
				Required: false,
				Value:    "converted",
			},
			&cli.StringFlag{
				Name:     reverseOutputDirectory,
				Usage:    "directory to write the restored containerlab YAML and device config files",
				Required: false,
				Value:    "restored",
			},
			&cli.StringFlag{
				Name: reverseFromSnapshot,
				Usage: "path to a snapshot ConfigMap YAML file; when set, device configs are sourced" +
					" from the snapshot instead of the output-directory ConfigMaps",
				Required: false,
				Value:    "",
			},
			&cli.BoolFlag{
				Name:     debug,
				Usage:    "enable debug logging",
				Required: false,
				Value:    false,
			},
			&cli.BoolFlag{
				Name:     quiet,
				Usage:    "disable all output",
				Required: false,
				Value:    false,
			},
		},
		Action: func(c *cli.Context) error {
			err := clabernetesclabverter.MustNewUnclabverter(
				c.String(reverseInputDirectory),
				c.String(reverseOutputDirectory),
				c.String(reverseFromSnapshot),
				c.Bool(debug),
				c.Bool(quiet),
			).Unclabvert()

			claberneteslogging.GetManager().Flush()

			return err
		},
	}
}
