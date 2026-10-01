package audit

import (
	"os"
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	in := []string{"rds", "modify-db-instance", "--master-user-password", "hunter2",
		"--secret-string=abc", "--db-instance-identifier", "db1", "--no-paginate"}
	got := strings.Join(Mask(in), " ")
	want := "rds modify-db-instance --master-user-password **** --secret-string=**** --db-instance-identifier db1 --no-paginate"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if in[3] != "hunter2" {
		t.Fatal("Mask must not modify its input")
	}
}

func TestWrite(t *testing.T) {
	t.Setenv("MEK_HOME", t.TempDir())
	if err := Write(Entry{Context: "uat", Command: []string{"aws", "--token", "x"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"--token","****"`) || strings.Contains(string(b), `"x"`) {
		t.Fatalf("audit line not masked: %s", b)
	}
}
