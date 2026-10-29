package db

import "testing"

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
