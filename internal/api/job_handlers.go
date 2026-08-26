package api

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	jobcore "github.com/sunxu/relay-station-control/internal/jobs"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	defaultJobPageSize = 50
	maximumJobPageSize = 200
)

var jobKindPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

func (s *Server) ListJobs(w http.ResponseWriter, r *http.Request, params ListJobsParams) {
	s.prepare(w, r, true)
	if !s.authorizeJobRead(w, r) {
		return
	}
	limit := defaultJobPageSize
	if params.Limit != nil {
		limit = *params.Limit
	}
	filters := jobstore.JobListFilters{}
	if params.JobKind != nil {
		filters.JobKind = string(*params.JobKind)
	}
	if params.Status != nil {
		filters.Status = string(*params.Status)
	}
	filters.CreatedFrom = params.CreatedFrom
	filters.CreatedTo = params.CreatedTo
	if limit < 1 || limit > maximumJobPageSize ||
		(filters.JobKind != "" && !jobKindPattern.MatchString(filters.JobKind)) ||
		(filters.Status != "" && !jobcore.Status(filters.Status).Valid()) ||
		(filters.CreatedFrom != nil && filters.CreatedTo != nil && filters.CreatedFrom.After(*filters.CreatedTo)) {
		s.jobReadError(w, r, jobstore.ErrInvalidJobQuery)
		return
	}

	var cursor *jobstore.JobCursor
	if params.Cursor != nil {
		if len(*params.Cursor) > 512 {
			s.jobReadError(w, r, jobstore.ErrInvalidJobCursor)
			return
		}
		decoded, err := s.jobCursor.Decode(*params.Cursor, filters)
		if err != nil {
			s.jobReadError(w, r, err)
			return
		}
		cursor = &decoded
	}
	page, err := s.jobs.ListJobs(r.Context(), filters, cursor, limit)
	if err != nil {
		s.jobReadError(w, r, err)
		return
	}
	items := make([]JobSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, jobSummary(item))
	}
	var nextCursor *string
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		encoded, encodeErr := s.jobCursor.Encode(filters, jobstore.JobCursor{CreatedAt: last.CreatedAt, JobID: last.JobID})
		if encodeErr != nil {
			s.jobReadError(w, r, encodeErr)
			return
		}
		nextCursor = &encoded
	}
	writeJSON(w, http.StatusOK, JobListResponse{Items: items, NextCursor: nextCursor})
}

func (s *Server) GetJob(w http.ResponseWriter, r *http.Request, jobID uuid.UUID) {
	s.prepare(w, r, true)
	if !s.authorizeJobRead(w, r) {
		return
	}
	detail, err := s.jobs.Job(r.Context(), jobID)
	if err != nil {
		s.jobReadError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, jobDetail(detail))
}

func (s *Server) authorizeJobRead(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := s.requireSession(w, r, false, ""); !ok {
		return false
	}
	if s.jobs == nil || s.jobCursor == nil {
		s.writeError(w, r, authn.ErrUnavailable)
		return false
	}
	return true
}

func (s *Server) jobReadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, jobstore.ErrInvalidJobQuery), errors.Is(err, jobstore.ErrInvalidJobCursor):
		s.writeError(w, r, authn.ErrInvalid)
	case errors.Is(err, jobstore.ErrJobNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorCodeNotFound, Message: "The job was not found.", RequestId: s.requestID(r)})
	default:
		slog.Warn("durable job read failed", "component", "jobs", "action", "read", "result", "database_unavailable")
		s.writeError(w, r, authn.ErrUnavailable)
	}
}

func jobSummary(value jobstore.JobSummary) JobSummary {
	return JobSummary{
		JobId: value.JobID, OperationId: value.OperationID, JobKind: JobKind(value.JobKind), Status: JobStatus(value.Status),
		AttemptCount: value.AttemptCount, MaxAttempts: value.MaxAttempts, AvailableAt: value.AvailableAt.UTC(),
		StartedAt: utcJobTime(value.StartedAt), CompletedAt: utcJobTime(value.CompletedAt), CancelRequested: value.CancelRequested,
		ErrorCode: jobErrorCode(value.ErrorCode), OutboxStatus: JobOutboxStatus(value.OutboxStatus),
		CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(),
	}
}

func jobDetail(value jobstore.JobDetail) JobDetail {
	summary := jobSummary(value.JobSummary)
	events := make([]JobLifecycleEvent, 0, len(value.Events))
	for _, event := range value.Events {
		events = append(events, JobLifecycleEvent{
			Sequence: int(event.Sequence), EventType: JobEventType(event.EventType), FromStatus: jobStatus(event.FromStatus),
			ToStatus: JobStatus(event.ToStatus), AttemptCount: event.Attempt, ActorType: JobActorType(event.ActorType),
			ReasonCode: jobReasonCode(event.ReasonCode), ErrorCode: jobErrorCode(event.ErrorCode), OccurredAt: event.OccurredAt.UTC(),
		})
	}
	return JobDetail{
		JobId: summary.JobId, OperationId: summary.OperationId, JobKind: summary.JobKind, Status: summary.Status,
		AttemptCount: summary.AttemptCount, MaxAttempts: summary.MaxAttempts, AvailableAt: summary.AvailableAt,
		StartedAt: summary.StartedAt, CompletedAt: summary.CompletedAt, CancelRequested: summary.CancelRequested,
		ErrorCode: summary.ErrorCode, OutboxStatus: summary.OutboxStatus, CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt,
		Events: events,
	}
}

func utcJobTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.UTC()
	return &converted
}

func jobStatus(value *string) *JobStatus {
	if value == nil {
		return nil
	}
	converted := JobStatus(*value)
	return &converted
}

func jobErrorCode(value *string) *JobErrorCode {
	if value == nil {
		return nil
	}
	converted := JobErrorCode(*value)
	return &converted
}

func jobReasonCode(value *string) *JobReasonCode {
	if value == nil {
		return nil
	}
	converted := JobReasonCode(*value)
	return &converted
}
