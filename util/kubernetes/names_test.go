package kubernetes_test

import (
	"testing"

	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
)

func TestSafeConcatNameKubernetes(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		expected string
	}{
		{
			name:     "simple",
			in:       []string{"afinename"},
			expected: "afinename",
		},
		{
			name:     "simple-multi-word",
			in:       []string{"a", "fine", "name"},
			expected: "a-fine-name",
		},
		{
			name: "over-max-len",
			in: []string{
				"a",
				"fine",
				"name",
				"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			},
			expected: "a-fine-name-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-8fa96d7",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.SafeConcatNameKubernetes(testCase.in...)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestSafeConcatNameMax(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		max      int
		expected string
	}{
		{
			name:     "simple",
			in:       []string{"afinename"},
			max:      30,
			expected: "afinename",
		},
		{
			name:     "simple-multi-word",
			in:       []string{"a", "fine", "name"},
			max:      30,
			expected: "a-fine-name",
		},
		{
			name: "over-max-len",
			in: []string{
				"a",
				"fine",
				"name",
				"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			},
			max:      30,
			expected: "a-fine-name-xxxxxxxxxx-8fa96d7",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.SafeConcatNameMax(testCase.in, testCase.max)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestEnforceDNSLabelConvention(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "simple",
			in:       "afinename",
			expected: "afinename",
		},
		{
			name:     "ending-with-digit",
			in:       "afinename1",
			expected: "afinename1",
		},
		{
			name:     "ending-with-hyphen",
			in:       "afinename-",
			expected: "afinenamez-14a1713",
		},
		{
			name:     "starting-with-digit",
			in:       "1afinename",
			expected: "1afinename",
		},
		{
			name:     "underscores",
			in:       "advanced_bgp_srsim",
			expected: "advanced-bgp-srsim-ae73657",
		},
		{
			name:     "uppercase",
			in:       "R4",
			expected: "r4",
		},
		{
			name:     "special-chars",
			in:       "afine.name",
			expected: "afine-name-6df0d13",
		},
		{
			name:     "special-chars-with-digit-boundaries",
			in:       "1afine.name2",
			expected: "1afine-name2-7601b99",
		},
		{
			// "safa-test1" has no invalid chars → no hash; "safa_test1" has underscore → hash.
			// They must not produce the same output.
			name:     "dash-vs-underscore-no-collision",
			in:       "safa-test1",
			expected: "safa-test1",
		},
		{
			name:     "dash-vs-underscore-underscore-variant",
			in:       "safa_test1",
			expected: "safa-test1-b0930a8",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.EnforceDNSLabelConvention(testCase.in)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}
