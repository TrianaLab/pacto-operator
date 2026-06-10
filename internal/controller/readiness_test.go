/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/readiness"
)

var readinessNow = time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

func pinReadinessClock(t *testing.T, at time.Time) {
	t.Helper()
	old := readinessClock
	readinessClock = func() time.Time { return at }
	t.Cleanup(func() { readinessClock = old })
}

func rdCheck(id string, weight int, expires string) contract.ReadinessCheck {
	return contract.ReadinessCheck{ID: id, Type: "url", Evidence: "https://example.com/" + id, Weight: weight, Expires: expires}
}

func rdContract(checks ...contract.ReadinessCheck) *contract.Contract {
	c := &contract.Contract{
		PactoVersion: "1.1",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
	}
	if len(checks) > 0 {
		c.Readiness = &contract.Readiness{Checks: checks}
	}
	return c
}

func drainEvents(rec *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// ---------- readinessCondition ----------

func TestReadinessCondition(t *testing.T) {
	cases := []struct {
		name       string
		res        *readiness.Result
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"passing", &readiness.Result{Passing: true, Score: 100, MinScore: 100, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionTrue, pactov1alpha1.ReasonReadinessSatisfied},
		{"below min score", &readiness.Result{Passing: false, Score: 60, MinScore: 100, ExpiredCount: 1, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionFalse, pactov1alpha1.ReasonReadinessBelowMinScore},
		{"invalid", &readiness.Result{Passing: false, Score: 50, MinScore: 100, InvalidCount: 1, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionFalse, pactov1alpha1.ReasonReadinessInvalid},
		{"passing despite expired (low minScore)", &readiness.Result{Passing: true, Score: 60, MinScore: 50, ExpiredCount: 1, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionTrue, pactov1alpha1.ReasonReadinessSatisfied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, msg := readinessCondition(tc.res)
			if status != tc.wantStatus {
				t.Errorf("status: want %s got %s", tc.wantStatus, status)
			}
			if reason != tc.wantReason {
				t.Errorf("reason: want %s got %s", tc.wantReason, reason)
			}
			if msg == "" {
				t.Error("expected non-empty message")
			}
		})
	}
}

// ---------- buildReadinessStatus ----------

func TestBuildReadinessStatus(t *testing.T) {
	eval := readiness.Evaluate(rdContract(
		rdCheck("dashboard", 60, "2026-12-31"),
		rdCheck("security-review", 40, "2026-01-15"),
	).Readiness, readinessNow)

	rs := buildReadinessStatus(eval)
	if rs.Score != 60 || rs.TotalWeight != 100 || rs.CurrentWeight != 60 {
		t.Errorf("unexpected summary: %+v", rs)
	}
	if rs.MinScore != 100 || rs.Passing {
		t.Errorf("expected default minScore 100 and not passing, got minScore=%d passing=%v", rs.MinScore, rs.Passing)
	}
	if rs.CurrentCount != 1 || rs.ExpiredCount != 1 {
		t.Errorf("unexpected counts: current=%d expired=%d", rs.CurrentCount, rs.ExpiredCount)
	}
	if len(rs.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(rs.Checks))
	}
	if rs.Checks[0].Status != "Current" || rs.Checks[0].DaysRemaining == nil {
		t.Errorf("expected first check Current with daysRemaining, got %+v", rs.Checks[0])
	}
	if rs.Checks[1].Status != "Expired" || rs.Checks[1].DaysRemaining != nil {
		t.Errorf("expected second check Expired without daysRemaining, got %+v", rs.Checks[1])
	}
}

// ---------- reconcileReadiness ----------

func newReadinessReconciler() (*PactoReconciler, *record.FakeRecorder) {
	rec := record.NewFakeRecorder(20)
	return &PactoReconciler{Recorder: rec}, rec
}

func TestReconcileReadiness_NoReadiness(t *testing.T) {
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	r.reconcileReadiness(pacto, rdContract(), false)

	if pacto.Status.Readiness != nil {
		t.Errorf("expected no readiness status, got %+v", pacto.Status.Readiness)
	}
	if meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied) != nil {
		t.Error("expected no readiness condition")
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no events, got %v", events)
	}
}

func TestReconcileReadiness_AllCurrent_NoEvent(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	r.reconcileReadiness(pacto, rdContract(rdCheck("dashboard", 100, "2026-12-31")), false)

	if pacto.Status.Readiness == nil || pacto.Status.Readiness.Score != 100 {
		t.Fatalf("expected readiness score 100, got %+v", pacto.Status.Readiness)
	}
	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != pactov1alpha1.ReasonReadinessSatisfied {
		t.Fatalf("expected True/ReadinessAllCurrent, got %+v", cond)
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no events when all current, got %v", events)
	}
}

func TestReconcileReadiness_Expired_EmitsWarningOnTransition(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// wasNotCurrent=false → transition into expired → warning event.
	r.reconcileReadiness(pacto, rdContract(rdCheck("security-review", 50, "2026-01-15")), false)

	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != pactov1alpha1.ReasonReadinessBelowMinScore {
		t.Fatalf("expected False/ReadinessExpired, got %+v", cond)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], pactov1alpha1.EventReadinessGateUnmet) || !strings.Contains(events[0], "Warning") {
		t.Errorf("expected one Warning ReadinessExpired event, got %v", events)
	}
}

func TestReconcileReadiness_Expired_NoEventWhenAlreadyExpired(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// wasNotCurrent=true → steady expired → no event (avoid spam).
	r.reconcileReadiness(pacto, rdContract(rdCheck("security-review", 50, "2026-01-15")), true)

	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no event for steady-expired, got %v", events)
	}
}

func TestReconcileReadiness_Recovered_EmitsNormalEvent(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// wasNotCurrent=true and now all current → recovery event.
	r.reconcileReadiness(pacto, rdContract(rdCheck("dashboard", 100, "2026-12-31")), true)

	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected True condition, got %+v", cond)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], pactov1alpha1.EventReadinessRecovered) || !strings.Contains(events[0], "Normal") {
		t.Errorf("expected one Normal ReadinessRecovered event, got %v", events)
	}
}

func TestReconcileReadiness_Invalid(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	r.reconcileReadiness(pacto, rdContract(rdCheck("bad", 50, "not-a-date")), false)

	if pacto.Status.Readiness == nil || pacto.Status.Readiness.Checks[0].Status != "Invalid" {
		t.Fatalf("expected an Invalid check, got %+v", pacto.Status.Readiness)
	}
	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != pactov1alpha1.ReasonReadinessInvalid {
		t.Fatalf("expected False/ReadinessInvalid, got %+v", cond)
	}
	if events := drainEvents(rec); len(events) != 1 {
		t.Errorf("expected one event for invalid transition, got %v", events)
	}
}
