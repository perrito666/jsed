package jsed

import (
	"bytes"
	"os"
	"testing"
)

func TestFilter(t *testing.T) {
	// test json generated thanks to https://json-generator.com/
	contents, err := os.ReadFile("filter_test.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		crit     FilterCriteria
		expected string
	}{
		{
			crit:     FilterCriteria{},
			expected: "",
		},
		{
			crit: FilterCriteria{
				Keys: []string{"about"},
			},
			expected: "",
		},
	} {
		{
			out := &bytes.Buffer{}
			f := bytes.NewReader(contents)
			err = Filter(f, out, &c.crit)
			if err != nil {
				t.Fatal(err)
			}
			outS := out.String()
			if outS != c.expected {
				t.Logf("expected: \n%s\ngot:\n%s", c.expected, outS)
				t.FailNow()
			}
		}
	}

}
