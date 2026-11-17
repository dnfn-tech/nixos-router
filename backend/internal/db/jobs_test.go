package db

import (
	"strings"
	"testing"
)

func TestRunningJobCanBeReadBeforeCompletion(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.CreateJob("running-job", "apply", `{"mode":"managed"}`); err != nil {
		t.Fatal(err)
	}
	job, ok, err := d.GetJob("running-job")
	if err != nil || !ok || job.Status != "running" {
		t.Fatalf("running job unreadable: %+v %v %v", job, ok, err)
	}
	jobs, err := d.ListJobs(20)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("running list unreadable: %+v %v", jobs, err)
	}
}

func TestFinishAndInterruptJobs(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, id := range []string{"done", "interrupted"} {
		if err = d.CreateJob(id, "apply", `{"mode":"managed-runtime"}`); err != nil {
			t.Fatal(err)
		}
	}
	if err = d.FinishJob("done", "success", `{"appliedRuntime":true}`, ""); err != nil {
		t.Fatal(err)
	}
	if err = d.InterruptRunningJobs(); err != nil {
		t.Fatal(err)
	}
	done, _, err := d.GetJob("done")
	if err != nil || done.Status != "success" || done.Payload != `{"appliedRuntime":true}` {
		t.Fatalf("completion lost: %+v %v", done, err)
	}
	interrupted, _, err := d.GetJob("interrupted")
	if err != nil || interrupted.Status != "failed" || interrupted.Error == "" {
		t.Fatalf("interrupted job not terminal: %+v %v", interrupted, err)
	}
}

func TestInterruptLegacyJobsPreservesExecutorJobs(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for id, payload := range map[string]string{"legacy": `{"mode":"managed-runtime"}`, "executor": `{"executor":true}`, "malformed": `{`, "done": `{}`} {
		if err := d.CreateJob(id, "apply", payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.FinishJob("done", "success", `{}`, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.InterruptLegacyRunningJobs(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy", "malformed"} {
		j, _, err := d.GetJob(id)
		if err != nil || j.Status != "failed" || j.Error == "" || !strings.Contains(j.Payload, `"rollbackStatus":"unknown"`) || !strings.Contains(j.Payload, `"stage":"interrupted"`) {
			t.Fatalf("legacy job not recovered: %+v %v", j, err)
		}
	}
	j, _, err := d.GetJob("executor")
	if err != nil || j.Status != "running" || j.Payload != `{"executor":true}` {
		t.Fatalf("executor job changed: %+v %v", j, err)
	}
	j, _, err = d.GetJob("done")
	if err != nil || j.Status != "success" || j.Payload != `{}` {
		t.Fatalf("terminal job changed: %+v %v", j, err)
	}
}
