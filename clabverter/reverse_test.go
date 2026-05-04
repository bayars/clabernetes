package clabverter_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	clabernetesclabverter "github.com/srl-labs/clabernetes/clabverter"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
)

func TestUnclabvert(t *testing.T) {
	cases := []struct {
		name             string
		// topologyFile is the containerlab input for the forward pass that produces the input dir.
		// When empty, inputDirectory must point directly to a pre-built fixture directory.
		topologyFile         string
		topologySpecFile     string
		destinationNamespace string
		insecureRegistries   string
		imagePullSecrets     string
		naming               string
		containerlabVersion  string
		fromSnapshotFile     string
	}{
		{
			// Round-trip: forward-convert the simple topology, then reverse it.
			name:                 "simple",
			topologyFile:         "test-fixtures/clabversiontest/clab.yaml",
			destinationNamespace: "notclabernetes",
			insecureRegistries:   "1.2.3.4",
			imagePullSecrets:     "regcred",
			naming:               "prefixed",
		},
		{
			// Round-trip with snapshot: forward-convert then reverse using a snapshot ConfigMap.
			name:                 "snapshot",
			topologyFile:         "test-fixtures/clabversiontest/clab.yaml",
			destinationNamespace: "notclabernetes",
			insecureRegistries:   "1.2.3.4",
			imagePullSecrets:     "regcred",
			naming:               "prefixed",
			fromSnapshotFile:     "test-fixtures/snapshot/snapshot-cm.yaml",
		},
		{
			// Snapshot-only: no Topology CR, just extract device config files.
			name:             "snapshot-only",
			fromSnapshotFile: "test-fixtures/snapshot/snapshot-cm.yaml",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Logf("%s: starting", testCase.name)

			forwardDir := fmt.Sprintf("test-fixtures/%s-forward-actual", testCase.name)
			actualDir := fmt.Sprintf("test-fixtures/%s-reverse-actual", testCase.name)

			for _, dir := range []string{forwardDir, actualDir} {
				if err := os.MkdirAll(
					dir,
					clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute,
				); err != nil {
					t.Fatalf("failed creating directory %q: %s", dir, err)
				}
			}

			defer func() {
				logManager := claberneteslogging.GetManager()
				logManager.DeleteLogger(clabernetesconstants.Clabverter)

				if !*clabernetestesthelper.SkipCleanup {
					for _, dir := range []string{forwardDir, actualDir} {
						if err := os.RemoveAll(dir); err != nil {
							t.Logf("failed cleaning up %q: %s", dir, err)
						}
					}
				}
			}()

			inputDir := forwardDir

			// Run the forward conversion to produce the input for the reverse pass.
			if testCase.topologyFile != "" {
				fwd := clabernetesclabverter.MustNewClabverter(
					testCase.topologyFile,
					testCase.topologySpecFile,
					forwardDir,
					testCase.destinationNamespace,
					testCase.naming,
					testCase.containerlabVersion,
					testCase.insecureRegistries,
					testCase.imagePullSecrets,
					false,
					false,
					true,
					false,
					"",
				)

				if err := fwd.Clabvert(); err != nil {
					t.Fatalf("forward clabvert failed: %s", err)
				}

				logManager := claberneteslogging.GetManager()
				logManager.DeleteLogger(clabernetesconstants.Clabverter)
			} else {
				// No forward pass — inputDir stays empty (snapshot-only case).
				inputDir = ""
			}

			unclabverter := clabernetesclabverter.MustNewUnclabverter(
				inputDir,
				actualDir,
				testCase.fromSnapshotFile,
				false,
				true,
			)

			if err := unclabverter.Unclabvert(); err != nil {
				t.Fatalf("unclabvert failed: %s", err)
			}

			actualFiles := readAllFiles(t, actualDir)

			if *clabernetestesthelper.Update {
				for relPath, content := range actualFiles {
					goldenPath := fmt.Sprintf(
						"test-fixtures/golden/reverse-%s/%s", testCase.name, relPath,
					)

					if err := os.MkdirAll(
						filepath.Dir(goldenPath),
						clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute,
					); err != nil {
						t.Fatalf("failed creating golden dir for %q: %s", goldenPath, err)
					}

					clabernetestesthelper.WriteTestFile(t, goldenPath, content)
				}

				return
			}

			for relPath, actualContent := range actualFiles {
				goldenPath := fmt.Sprintf("golden/reverse-%s/%s", testCase.name, relPath)

				expected := clabernetestesthelper.ReadTestFixtureFile(t, goldenPath)

				if !bytes.Equal(actualContent, expected) {
					clabernetestesthelper.FailOutput(t, actualContent, expected)
				}
			}

			// Ensure no extra files were produced that are not in the golden set.
			goldenFiles := readAllFiles(t, fmt.Sprintf("test-fixtures/golden/reverse-%s", testCase.name))

			if len(actualFiles) != len(goldenFiles) {
				actualKeys := sortedKeys(actualFiles)
				goldenKeys := sortedKeys(goldenFiles)

				t.Fatalf(
					"file count mismatch: got %d file(s) %v, want %d file(s) %v",
					len(actualFiles), actualKeys,
					len(goldenFiles), goldenKeys,
				)
			}
		})
	}
}

// readAllFiles walks dir recursively and returns a map of relative path → content.
func readAllFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	result := map[string][]byte{}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("failed resolving %q: %s", dir, err)
	}

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if info.IsDir() {
			return nil
		}

		content, readErr := os.ReadFile(path) //nolint:gosec
		if readErr != nil {
			return readErr
		}

		rel, relErr := filepath.Rel(absDir, path)
		if relErr != nil {
			return relErr
		}

		result[rel] = content

		return nil
	})
	if err != nil {
		t.Fatalf("failed walking %q: %s", dir, err)
	}

	return result
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))

	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
