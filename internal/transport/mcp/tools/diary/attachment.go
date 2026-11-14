package diary

import (
	"context"

	corediary "github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

func newAddDiaryAttachment(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "addDiaryAttachment",
		definition: mcp.NewTool(
			"addDiaryAttachment",
			mcp.WithDescription("Attach a file the user uploaded to a committed diary entry. The bytes travel as base64 because MCP tool arguments are JSON. Stores the file in the user's workspace and returns its file_id. Call only for a file the user just chose in the diary UI, not for a file the model invented."),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("entry_id", mcp.Required(), mcp.Description("Committed entry id.")),
			mcp.WithInteger("expected_revision", mcp.Required(), mcp.Description("Current revision; mismatches return 409.")),
			mcp.WithString("name", mcp.Required(), mcp.Description("Original file name, used for display only.")),
			mcp.WithString("mime", mcp.Description("Declared MIME type. The server sniffs the bytes when this is missing or implausible.")),
			mcp.WithString("content_base64", mcp.Required(), mcp.Description("File bytes, standard base64. Maximum 8 MiB decoded.")),
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
			entryID, err := request.RequireString("entry_id")
			if err != nil {
				return nil, argError("entry_id is required")
			}
			name, err := request.RequireString("name")
			if err != nil {
				return nil, argError("name is required")
			}
			content, err := request.RequireString("content_base64")
			if err != nil {
				return nil, argError("content_base64 is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.AddAttachment(ctx, fs, corediary.AddAttachmentIn{
				RequestID:        requestID,
				EntryID:          entryID,
				ExpectedRevision: rev,
				Name:             name,
				MIME:             optionalString(request, "mime"),
				ContentBase64:    content,
			})
		},
	}
}

func newRemoveDiaryAttachment(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "removeDiaryAttachment",
		definition: mcp.NewTool(
			"removeDiaryAttachment",
			mcp.WithDescription("Remove one uploaded file from a committed diary entry and delete its bytes. Call only after the user confirms in the diary UI."),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("entry_id", mcp.Required(), mcp.Description("Committed entry id.")),
			mcp.WithString("file_id", mcp.Required(), mcp.Description("Attachment id returned by addDiaryAttachment.")),
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
			entryID, err := request.RequireString("entry_id")
			if err != nil {
				return nil, argError("entry_id is required")
			}
			fileID, err := request.RequireString("file_id")
			if err != nil {
				return nil, argError("file_id is required")
			}
			rev, err := requireRevision(request)
			if err != nil {
				return nil, err
			}
			return svc.RemoveAttachment(ctx, fs, corediary.RemoveAttachmentIn{
				RequestID:        requestID,
				EntryID:          entryID,
				FileID:           fileID,
				ExpectedRevision: rev,
			})
		},
	}
}
