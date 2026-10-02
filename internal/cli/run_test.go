package cli

import (
	"strings"
	"testing"
)

func TestTakeGlobalFlags(t *testing.T) {
	cases := []struct {
		in, rest, ctx, confirm string
		yes                    bool
	}{
		{"s3 ls", "s3 ls", "", "", false},
		{"-c uat s3 ls", "s3 ls", "uat", "", false},
		{"--context=uat -y ec2 describe-instances", "ec2 describe-instances", "uat", "", true},
		{"--confirm prod -c prod ec2 terminate-instances --yes", "ec2 terminate-instances --yes", "prod", "prod", false},
		{"--region x s3 ls", "--region x s3 ls", "", "", false}, // aws flags are left alone
	}
	for _, c := range cases {
		a := &app{}
		rest, err := a.takeGlobalFlags(strings.Fields(c.in))
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if strings.Join(rest, " ") != c.rest || a.opts.context != c.ctx || a.opts.confirm != c.confirm || a.opts.yes != c.yes {
			t.Errorf("%q -> rest=%q opts=%+v", c.in, rest, a.opts)
		}
	}
	if _, err := (&app{}).takeGlobalFlags([]string{"-c"}); err == nil {
		t.Error("expected error for -c without value")
	}
}
