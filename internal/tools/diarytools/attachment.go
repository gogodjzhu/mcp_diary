package diarytools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/diary"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

func newAttachDiaryMedia(workspaces tools.WorkspaceProvider, svc *diary.Service) tools.Tool {
	return diaryTool{
		name: "attachDiaryMedia",
		definition: mcp.NewTool(
			"attachDiaryMedia",
			mcp.WithDescription("Attach an image or video from a workspace file to a draft session or committed entry. Files are stored under .mcp-diary/attachments/<diary-id>/ with a generated safe name. Allowed: jpg, jpeg, png, gif, webp, mp4, webm, mov."),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Description("Draft session id. Provide exactly one of session_id or entry_id.")),
			mcp.WithString("entry_id", mcp.Description("Committed entry id. Provide exactly one of session_id or entry_id.")),
			mcp.WithString("source_path", mcp.Required(), mcp.Description("Workspace-relative path to the image or video file.")),
			mcp.WithString("filename", mcp.Description("Original filename used to pick the extension. Defaults to the source_path basename.")),
			mcp.WithString("mime_type", mcp.Description("Optional declared MIME type; must match the file extension and content.")),
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
			sourcePath, err := request.RequireString("source_path")
			if err != nil {
				return nil, argError("source_path is required")
			}
			return svc.AttachMedia(root, diary.AttachMediaIn{
				RequestID:  requestID,
				SessionID:  optionalString(request, "session_id"),
				EntryID:    optionalString(request, "entry_id"),
				Filename:   optionalString(request, "filename"),
				MIMEType:   optionalString(request, "mime_type"),
				SourcePath: sourcePath,
			})
		},
	}
}

func newRemoveDiaryAttachment(workspaces tools.WorkspaceProvider, svc *diary.Service) tools.Tool {
	return diaryTool{
		name: "removeDiaryAttachment",
		definition: mcp.NewTool(
			"removeDiaryAttachment",
			mcp.WithDescription("Remove an image or video attachment from a draft session or committed entry and delete the stored file."),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("session_id", mcp.Description("Draft session id. Provide exactly one of session_id or entry_id.")),
			mcp.WithString("entry_id", mcp.Description("Committed entry id. Provide exactly one of session_id or entry_id.")),
			mcp.WithString("attachment_id", mcp.Required(), mcp.Description("Attachment id to remove.")),
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
			attID, err := request.RequireString("attachment_id")
			if err != nil {
				return nil, argError("attachment_id is required")
			}
			return svc.RemoveAttachment(root, diary.RemoveAttachmentIn{
				RequestID:    requestID,
				SessionID:    optionalString(request, "session_id"),
				EntryID:      optionalString(request, "entry_id"),
				AttachmentID: attID,
			})
		},
	}
}
