package ngalert

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/registry"
	"github.com/grafana/grafana/pkg/services/ngalert/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/benbjohnson/clock"
)

type evalAppliedInfo struct {
	alertDefKey alertDefinitionKey
	now         time.Time
}

func TestAlertingTicker(t *testing.T) {
	ng := setupTestEnv(t)
	t.Cleanup(registry.ClearOverrides)

	alerts := make([]*AlertDefinition, 0)
	// create alert definition with zero interval (should never run)
	alerts = append(alerts, createTestAlertDefinition(t, ng, 0))

	// create alert definition with one second interval
	alerts = append(alerts, createTestAlertDefinition(t, ng, 1))

	evalAppliedCh := make(chan evalAppliedInfo, len(alerts))
	stopAppliedCh := make(chan alertDefinitionKey, len(alerts))

	mockedClock := clock.NewMock()
	baseInterval := time.Second
	store := storeImpl{baseInterval: baseInterval, SQLStore: ng.SQLStore}
	schefCfg := schedulerCfg{
		c:               mockedClock,
		baseInterval:    baseInterval,
		logger:          log.New("ngalert.schedule.test"),
		evaluator:       eval.Evaluator{Cfg: ng.Cfg},
		definitionStore: store,
		instanceStore:   store,
		evalApplied: func(alertDefKey alertDefinitionKey, now time.Time) {
			evalAppliedCh <- evalAppliedInfo{alertDefKey: alertDefKey, now: now}
		},
		stopApplied: func(alertDefKey alertDefinitionKey) {
			stopAppliedCh <- alertDefKey
		},
	}
	ng.schedule = newScheduler(schefCfg)

	ctx := context.Background()
	go func() {
		err := ng.schedule.Ticker(ctx)
		require.NoError(t, err)
	}()
	runtime.Gosched()

	expectedAlertDefinitionsEvaluated := []alertDefinitionKey{alerts[1].getKey()}
	t.Run(fmt.Sprintf("on 1st tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	// change alert definition interval to three seconds
	var threeSecInterval int64 = 3
	err := ng.definitionStore.updateAlertDefinition(&updateAlertDefinitionCommand{
		UID:             alerts[0].UID,
		IntervalSeconds: &threeSecInterval,
		OrgID:           alerts[0].OrgID,
	})
	require.NoError(t, err)
	t.Logf("alert definition: %v interval reset to: %d", alerts[0].getKey(), threeSecInterval)

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{alerts[1].getKey()}
	t.Run(fmt.Sprintf("on 2nd tick alert definition: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{alerts[1].getKey(), alerts[0].getKey()}
	t.Run(fmt.Sprintf("on 3rd tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{alerts[1].getKey()}
	t.Run(fmt.Sprintf("on 4th tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	err = ng.definitionStore.deleteAlertDefinitionByUID(&deleteAlertDefinitionByUIDCommand{UID: alerts[1].UID, OrgID: alerts[1].OrgID})
	require.NoError(t, err)
	t.Logf("alert definition: %v deleted", alerts[1].getKey())

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{}
	t.Run(fmt.Sprintf("on 5th tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})
	expectedAlertDefinitionsStopped := []alertDefinitionKey{alerts[1].getKey()}
	t.Run(fmt.Sprintf("on 5th tick alert definitions: %s should be stopped", concatenate(expectedAlertDefinitionsStopped)), func(t *testing.T) {
		assertStopRun(t, stopAppliedCh, expectedAlertDefinitionsStopped...)
	})

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{alerts[0].getKey()}
	t.Run(fmt.Sprintf("on 6th tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	// create alert definition with one second interval
	alerts = append(alerts, createTestAlertDefinition(t, ng, 1))

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{alerts[2].getKey()}
	t.Run(fmt.Sprintf("on 7th tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	// pause alert definition
	err = ng.updateAlertDefinitionPaused(&updateAlertDefinitionPausedCommand{UIDs: []string{alerts[2].UID}, OrgID: alerts[2].OrgID, Paused: true})
	require.NoError(t, err)
	t.Logf("alert definition: %v paused", alerts[2].getKey())

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{}
	t.Run(fmt.Sprintf("on 8th tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})

	expectedAlertDefinitionsStopped = []alertDefinitionKey{alerts[2].getKey()}
	t.Run(fmt.Sprintf("on 8th tick alert definitions: %s should be stopped", concatenate(expectedAlertDefinitionsStopped)), func(t *testing.T) {
		assertStopRun(t, stopAppliedCh, expectedAlertDefinitionsStopped...)
	})

	// unpause alert definition
	err = ng.updateAlertDefinitionPaused(&updateAlertDefinitionPausedCommand{UIDs: []string{alerts[2].UID}, OrgID: alerts[2].OrgID, Paused: false})
	require.NoError(t, err)
	t.Logf("alert definition: %v unpaused", alerts[2].getKey())

	expectedAlertDefinitionsEvaluated = []alertDefinitionKey{alerts[0].getKey(), alerts[2].getKey()}
	t.Run(fmt.Sprintf("on 9th tick alert definitions: %s should be evaluated", concatenate(expectedAlertDefinitionsEvaluated)), func(t *testing.T) {
		tick := advanceClock(t, mockedClock)
		assertEvalRun(t, evalAppliedCh, tick, expectedAlertDefinitionsEvaluated...)
	})
}

func assertEvalRun(t *testing.T, ch <-chan evalAppliedInfo, tick time.Time, keys ...alertDefinitionKey) {
	timeout := time.After(time.Second)

	expected := make(map[alertDefinitionKey]struct{}, len(keys))
	for _, k := range keys {
		expected[k] = struct{}{}
	}

	for {
		select {
		case info := <-ch:
			_, ok := expected[info.alertDefKey]
			t.Logf("alert definition: %v evaluated at: %v", info.alertDefKey, info.now)
			assert.True(t, ok)
			assert.Equal(t, tick, info.now)
			delete(expected, info.alertDefKey)
			if len(expected) == 0 {
				return
			}
		case <-timeout:
			if len(expected) == 0 {
				return
			}
			t.Fatal("cycle has expired")
		}
	}
}

func assertStopRun(t *testing.T, ch <-chan alertDefinitionKey, keys ...alertDefinitionKey) {
	timeout := time.After(time.Second)

	expected := make(map[alertDefinitionKey]struct{}, len(keys))
	for _, k := range keys {
		expected[k] = struct{}{}
	}

	for {
		select {
		case alertDefKey := <-ch:
			_, ok := expected[alertDefKey]
			t.Logf("alert definition: %v stopped", alertDefKey)
			assert.True(t, ok)
			delete(expected, alertDefKey)
			if len(expected) == 0 {
				return
			}
		case <-timeout:
			if len(expected) == 0 {
				return
			}
			t.Fatal("cycle has expired")
		}
	}
}

func advanceClock(t *testing.T, mockedClock *clock.Mock) time.Time {
	mockedClock.Add(time.Second)
	return mockedClock.Now()
	// t.Logf("Tick: %v", mockedClock.Now())
}

func concatenate(keys []alertDefinitionKey) string {
	s := make([]string, len(keys))
	for _, k := range keys {
		s = append(s, k.String())
	}
	return fmt.Sprintf("[%s]", strings.Join(s, ","))
}
