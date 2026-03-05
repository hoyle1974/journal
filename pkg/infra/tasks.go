package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/google/uuid"
	"github.com/jackstrohm/jot/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// EnqueueTask creates a Cloud Task that POSTs the given payload as JSON to the API at endpoint.
func EnqueueTask(ctx context.Context, cfg *config.Config, endpoint string, payload map[string]interface{}) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	baseURL := strings.TrimSuffix(cfg.JotAPIBaseURL, "/")
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

	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", cfg.GoogleCloudProject, cfg.CloudTasksLocation, cfg.CloudTasksQueue)
	taskID := fmt.Sprintf("jot-%s", uuid.New().String()[:8])
	taskName := fmt.Sprintf("%s/tasks/%s", parent, taskID)
	url := baseURL + endpoint

	headers := map[string]string{
		"Content-Type": "application/json",
		"X-API-Key":    cfg.JotAPIKey,
	}
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
