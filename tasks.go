package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// EnqueueTask creates a Cloud Task that POSTs the given payload as JSON to the API at endpoint.
// The task is sent to the configured queue and will hit baseURL + endpoint with X-API-Key header.
// Returns an error if JOT_API_URL is not set, JSON marshal fails, or CreateTask fails.
func EnqueueTask(ctx context.Context, endpoint string, payload map[string]interface{}) error {
	baseURL := strings.TrimSuffix(JotAPIBaseURL, "/")
	if baseURL == "" {
		return fmt.Errorf("JOT_API_URL is not set; cannot enqueue task to %s", endpoint)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal task payload: %w", err)
	}

	tasksClient, err := cloudtasks.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create Cloud Tasks client: %w", err)
	}
	defer tasksClient.Close()

	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", GoogleCloudProject, CloudTasksLocation, CloudTasksQueue)
	taskID := fmt.Sprintf("jot-%s", uuid.New().String()[:8])
	taskName := fmt.Sprintf("%s/tasks/%s", parent, taskID)
	url := baseURL + endpoint

	headers := map[string]string{
		"Content-Type": "application/json",
		"X-API-Key":    JotAPIKey,
	}
	// Propagate trace context so the task's request continues the same trace when it hits the API.
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for k, v := range carrier {
		headers[k] = v
	}

	task := &cloudtaskspb.Task{
		Name: taskName,
		MessageType: &cloudtaskspb.Task_HttpRequest{
			HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        url,
				Headers:    headers,
				Body:       body,
			},
		},
	}

	_, err = tasksClient.CreateTask(ctx, &cloudtaskspb.CreateTaskRequest{
		Parent: parent,
		Task:   task,
	})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}
