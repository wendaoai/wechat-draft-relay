package wechatdraft

import "time"

func validState(st *State, r *Receipt) bool {
	if st.SchemaVersion != 1 || st.AppID != r.AppID || st.UUID != r.UUID || st.RequestHash != r.RequestHash || st.TaskTokenHash != r.TaskTokenHash || st.Attempt < 1 || !st.CreatedAt.Equal(r.AcceptedAt) || !st.AcceptedAt.Equal(r.AcceptedAt) || st.UpdatedAt.IsZero() || st.QueuedAt.IsZero() {
		return false
	}
	if !wordSet("queued running succeeded failed cancelled uncertain")[st.Status] || !wordSet("accepted getting_token uploading_cover uploading_body_images preparing_content submitting_draft recovery_unknown done")[st.Phase] {
		return false
	}
	if st.Attempt == 1 && st.LastRetry != nil || st.Attempt > 1 && (st.LastRetry == nil || !OperationID(st.LastRetry.RequestID) || st.LastRetry.ExpectedAttempt != st.Attempt-1 || st.LastRetry.AcceptedAttempt != st.Attempt) {
		return false
	}
	p := st.Progress
	if p.Total != len(r.Files)+1 || p.Completed < 1 || p.Completed > p.Total || p.Percent != p.Completed*100/p.Total {
		return false
	}
	if terminal(st) {
		if st.FinishedAt == nil || st.ExpiresAt == nil || !st.ExpiresAt.Equal(st.FinishedAt.Add(24*time.Hour)) {
			return false
		}
	} else if st.FinishedAt != nil || st.ExpiresAt != nil || st.Result != nil || st.Error != nil || p.Completed == p.Total {
		return false
	}
	if st.Status == "queued" && st.Phase != "accepted" {
		return false
	}
	if st.Status == "running" && !wordSet("getting_token uploading_cover uploading_body_images preparing_content submitting_draft")[st.Phase] {
		return false
	}
	if st.Status == "succeeded" {
		return st.Result != nil && validMediaID(st.Result.MediaID) && st.Phase == "done" && st.Error == nil && p.Completed == p.Total && st.Result.CompletedAt.Equal(*st.FinishedAt)
	}
	return st.Result == nil && st.Phase != "done" && (!terminal(st) || st.Error != nil)
}
