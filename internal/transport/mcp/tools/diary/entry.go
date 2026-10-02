package diary

import (
	"context"

	corediary "github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

func newGetDiaryEntry(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "getDiaryEntry",
		definition: mcp.NewTool(
			"getDiaryEntry",
			mcp.WithDescription("Read a committed diary entry by entry_id."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("entry_id", mcp.Required(), mcp.Description("Committed entry id.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			root, err := workspacePath(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			entryID, err := request.RequireString("entry_id")
			if err != nil {
				return nil, argError("entry_id is required")
			}
			return svc.GetEntry(root, corediary.GetEntryIn{
				RequestID: requestID,
				EntryID:   entryID,
			})
		},
	}
}

func newUpdateDiaryEntry(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "updateDiaryEntry",
		definition: mcp.NewTool(
			"updateDiaryEntry",
			mcp.WithDescription("Replace the full content of a committed diary entry. Does not reopen a draft."),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("entry_id", mcp.Required(), mcp.Description("Committed entry id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
			mcp.WithString("content", mcp.Required(), mcp.Description("Full replacement entry text.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			if err := requireWritable(workspaces); err != nil {
				return nil, err
			}
			root, err := workspacePath(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			entryID, err := request.RequireString("entry_id")
			if err != nil {
				return nil, argError("entry_id is required")
			}
			content, err := request.RequireString("content")
			if err != nil {
				return nil, argError("content is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.UpdateEntry(root, corediary.UpdateEntryIn{
				RequestID:        requestID,
				EntryID:          entryID,
				ExpectedRevision: rev,
				Content:          content,
			})
		},
	}
}

func newListDiaryEntries(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "listDiaryEntries",
		definition: mcp.NewTool(
			"listDiaryEntries",
			mcp.WithDescription("List committed diary entries by business date range with pagination. Does not include drafts."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("diary_date_from", mcp.Description("Inclusive start date YYYY-MM-DD.")),
			mcp.WithString("diary_date_to", mcp.Description("Inclusive end date YYYY-MM-DD.")),
			mcp.WithInteger("page", mcp.Description("1-based page number. Defaults to 1.")),
			mcp.WithInteger("page_size", mcp.Description("Page size. Defaults to 20, max 100.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			root, err := workspacePath(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			return svc.ListEntries(root, corediary.ListEntriesIn{
				RequestID:     requestID,
				DiaryDateFrom: optionalString(request, "diary_date_from"),
				DiaryDateTo:   optionalString(request, "diary_date_to"),
				Page:          request.GetInt("page", 1),
				PageSize:      request.GetInt("page_size", 20),
			})
		},
	}
}

func newDeleteDiaryEntry(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "deleteDiaryEntry",
		definition: mcp.NewTool(
			"deleteDiaryEntry",
			mcp.WithDescription("Permanently delete a committed diary entry. Confirm with the user before calling."),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("entry_id", mcp.Required(), mcp.Description("Committed entry id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			if err := requireWritable(workspaces); err != nil {
				return nil, err
			}
			root, err := workspacePath(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			entryID, err := request.RequireString("entry_id")
			if err != nil {
				return nil, argError("entry_id is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.DeleteEntry(root, corediary.DeleteEntryIn{
				RequestID:        requestID,
				EntryID:          entryID,
				ExpectedRevision: rev,
			})
		},
	}
}
