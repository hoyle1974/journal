package api

import (
	"fmt"
	"net/http"
	"time"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/google/uuid"
	"github.com/jackstrohm/jot/pkg/infra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func handleWebhook(s *Server, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	resourceState := r.Header.Get("X-Goog-Resource-State")
	LogHandlerRequest(ctx, r.Method, path, "resource_state", resourceState)
	ctx, span := infra.StartSpan(ctx, "webhook.gdrive")
	defer span.End()
	if resourceState == "sync" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "sync acknowledged")
		WriteJSON(w, http.StatusOK, map[string]string{"status": "sync acknowledged"})
		return
	}
	span.SetAttributes(map[string]string{"resource_state": resourceState})
	if resourceState != "change" && resourceState != "update" {
		infra.LoggerFrom(ctx).Info("webhook ignored", "resource_state", resourceState)
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "resource_state")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ignored", "reason": fmt.Sprintf("resource_state=%s", resourceState),
		})
		return
	}
	if s.Config.SyncGDocURL == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", "SYNC_GDOC_URL not configured")
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "SYNC_GDOC_URL not configured"})
		return
	}
	debounceSeconds := 5
	tasksClient, err := cloudtasks.NewClient(ctx)
	if err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create Tasks client: %v", err)})
		return
	}
	defer tasksClient.Close()
	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", s.Config.GoogleCloudProject, s.Config.CloudTasksLocation, s.Config.CloudTasksQueue)
	fsClient, err := s.Backend.GetFirestoreClient(ctx)
	if err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to get Firestore client: %v", err)})
		return
	}
	debounceRef := fsClient.Collection(s.Backend.SystemCollection()).Doc("sync_debounce")
	if doc, err := debounceRef.Get(ctx); err == nil && doc.Exists() {
		data := doc.Data()
		if oldTaskName, ok := data["task_name"].(string); ok && oldTaskName != "" {
			if err := tasksClient.DeleteTask(ctx, &cloudtaskspb.DeleteTaskRequest{Name: oldTaskName}); err != nil {
				infra.LoggerFrom(ctx).Debug("failed to delete old task (may have already executed)", "error", err)
			} else {
				infra.LoggerFrom(ctx).Debug("cancelled previous sync task")
			}
		}
	}
	taskID := fmt.Sprintf("jot-sync-%s", uuid.New().String()[:8])
	taskName := fmt.Sprintf("%s/tasks/%s", parent, taskID)
	scheduleTime := time.Now().Add(time.Duration(debounceSeconds) * time.Second)
	task := &cloudtaskspb.Task{
		Name: taskName,
		MessageType: &cloudtaskspb.Task_HttpRequest{
			HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        s.Config.SyncGDocURL,
				Headers:    map[string]string{"Content-Type": "application/json", "X-API-Key": s.Config.JotAPIKey},
			},
		},
		ScheduleTime: timestamppb.New(scheduleTime),
	}
	_, err = tasksClient.CreateTask(ctx, &cloudtaskspb.CreateTaskRequest{Parent: parent, Task: task})
	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Error("webhook failed to schedule sync", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create task: %v", err)})
		return
	}
	infra.LoggerFrom(ctx).Info("webhook", "event", "Drive change, sync scheduled", "delay_seconds", debounceSeconds, "task_id", taskID)
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "scheduled", "task_id", taskID, "delay_seconds", debounceSeconds)
	s.Backend.SubmitAsync(ctx, func() {
		if _, err := debounceRef.Set(ctx, map[string]interface{}{
			"task_name": taskName, "scheduled_time": scheduleTime.Format(time.RFC3339),
		}); err != nil {
			infra.LoggerFrom(ctx).Warn("failed to store debounce state", "error", err)
		}
	})
	infra.LoggerFrom(ctx).Debug("webhook completed", "duration_ms", time.Since(startTime).Milliseconds())
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status": "scheduled", "message": fmt.Sprintf("Sync scheduled for %d seconds from now", debounceSeconds),
		"scheduled_time": scheduleTime.Format(time.RFC3339),
	})
}
