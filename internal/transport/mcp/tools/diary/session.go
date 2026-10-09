package diary

import (
	"context"

	corediary "github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/apps"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

func newCreateDiarySession(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "createDiarySession",
		definition: mcp.NewTool(
			"createDiarySession",
			mcp.WithDescription("Create a diary draft for a business date, or resume the unfinished draft for that date. Identity comes from OAuth; do not pass user_id. Same user and diary_date return the existing draft. A date that already has a committed entry cannot start another draft."),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("diary_date", mcp.Description("Business date YYYY-MM-DD. Defaults to today in the server timezone.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			if err := requireWritable(fs); err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			return svc.CreateSession(ctx, fs, corediary.CreateSessionIn{
				RequestID: requestID,
				DiaryDate: optionalString(request, "diary_date"),
			})
		},
	}
}

func newAppendDiarySession(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "appendDiarySession",
		definition: mcp.NewTool(
			"appendDiarySession",
			mcp.WithDescription("Append Agent-prepared text to a draft diary session. Returns the full current draft content. Only draft sessions can be appended."),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Required(), mcp.Description("Draft session id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Text fragment to append.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			if err := requireWritable(fs); err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			sessionID, err := request.RequireString("session_id")
			if err != nil {
				return nil, argError("session_id is required")
			}
			content, err := request.RequireString("content")
			if err != nil {
				return nil, argError("content is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.AppendSession(ctx, fs, corediary.AppendSessionIn{
				RequestID:        requestID,
				SessionID:        sessionID,
				ExpectedRevision: rev,
				Content:          content,
			})
		},
	}
}

func newGetDiarySession(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "getDiarySession",
		definition: apps.WithWidget(mcp.NewTool(
			"getDiarySession",
			mcp.WithDescription("Read a diary draft by session_id so the Agent can continue after context loss."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Required(), mcp.Description("Draft session id.")),
		)),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			sessionID, err := request.RequireString("session_id")
			if err != nil {
				return nil, argError("session_id is required")
			}
			return svc.GetSession(ctx, fs, corediary.GetSessionIn{
				RequestID: requestID,
				SessionID: sessionID,
			})
		},
	}
}

func newUpdateDiarySession(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "updateDiarySession",
		definition: mcp.NewTool(
			"updateDiarySession",
			mcp.WithDescription("Replace the full draft content. Use this when the user corrects earlier text. Only draft sessions can be updated."),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Required(), mcp.Description("Draft session id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Full replacement draft text.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			if err := requireWritable(fs); err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			sessionID, err := request.RequireString("session_id")
			if err != nil {
				return nil, argError("session_id is required")
			}
			content, err := request.RequireString("content")
			if err != nil {
				return nil, argError("content is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.UpdateSession(ctx, fs, corediary.UpdateSessionIn{
				RequestID:        requestID,
				SessionID:        sessionID,
				ExpectedRevision: rev,
				Content:          content,
			})
		},
	}
}

func newCommitDiarySession(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "commitDiarySession",
		definition: mcp.NewTool(
			"commitDiarySession",
			mcp.WithDescription("Commit a draft as the formal diary for that date after the user confirms. Deletes the draft. Call only when the user explicitly says OK/save. Repeat with the same request_id returns the same entry_id."),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Required(), mcp.Description("Draft session id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			if err := requireWritable(fs); err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			sessionID, err := request.RequireString("session_id")
			if err != nil {
				return nil, argError("session_id is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.CommitSession(ctx, fs, corediary.CommitSessionIn{
				RequestID:        requestID,
				SessionID:        sessionID,
				ExpectedRevision: rev,
			})
		},
	}
}

func newDiscardDiarySession(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "discardDiarySession",
		definition: mcp.NewTool(
			"discardDiarySession",
			mcp.WithDescription("Discard an uncommitted diary draft at the user's request. This is a terminal action."),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Required(), mcp.Description("Draft session id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			if err := requireWritable(fs); err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			sessionID, err := request.RequireString("session_id")
			if err != nil {
				return nil, argError("session_id is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.DiscardSession(ctx, fs, corediary.DiscardSessionIn{
				RequestID:        requestID,
				SessionID:        sessionID,
				ExpectedRevision: rev,
			})
		},
	}
}
