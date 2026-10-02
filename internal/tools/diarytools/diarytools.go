package diarytools

import (
	"context"
	"strings"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type diaryTool struct {
	name       string
	definition mcp.Tool
	handle     func(ctx context.Context, request mcp.CallToolRequest) (any, error)
}

func (t diaryTool) Name() string         { return t.name }
func (t diaryTool) Definition() mcp.Tool { return t.definition }

func (t diaryTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	data, err := t.handle(ctx, request)
	if err != nil {
		return tools.Result(failFrom(err)), nil
	}
	return tools.Result(diary.OK(data)), nil
}

func All(workspaces tools.WorkspaceProvider, svc *diary.Service) []tools.Tool {
	return []tools.Tool{
		newCreateDiarySession(workspaces, svc),
		newAppendDiarySession(workspaces, svc),
		newGetDiarySession(workspaces, svc),
		newUpdateDiarySession(workspaces, svc),
		newCommitDiarySession(workspaces, svc),
		newDiscardDiarySession(workspaces, svc),
		newGetDiaryEntry(workspaces, svc),
		newUpdateDiaryEntry(workspaces, svc),
		newListDiaryEntries(workspaces, svc),
		newDeleteDiaryEntry(workspaces, svc),
	}
}

func workspacePath(ctx context.Context, workspaces tools.WorkspaceProvider) (string, error) {
	fs, err := workspaces.Filesystem(ctx)
	if err != nil {
		return "", mapWorkspaceErr(err)
	}
	return fs.Root(), nil
}

func requireWritable(workspaces tools.WorkspaceProvider) error {
	type readOnlyAware interface{ ReadOnly() bool }
	if ro, ok := workspaces.(readOnlyAware); ok && ro.ReadOnly() {
		return &diary.Error{Code: 403, Message: "workspace is read-only"}
	}
	return nil
}

func optionalString(request mcp.CallToolRequest, name string) string {
	return strings.TrimSpace(request.GetString(name, ""))
}

func requireRequestID(request mcp.CallToolRequest) (string, error) {
	v, err := request.RequireString("request_id")
	if err != nil {
		return "", argError("request_id is required")
	}
	return v, nil
}

func requireRevision(request mcp.CallToolRequest) (int, error) {
	v, err := request.RequireInt("expected_revision")
	if err != nil {
		return 0, argError("expected_revision is required")
	}
	return v, nil
}

func argError(message string) error {
	return &diary.Error{Code: 400, Message: message}
}

func failFrom(err error) diary.Envelope {
	if de, ok := diary.IsError(err); ok {
		return diary.Fail(de.Code, de.Message)
	}
	return diary.Fail(500, err.Error())
}

func mapWorkspaceErr(err error) error {
	if err == nil {
		return nil
	}
	if de, ok := diary.IsError(err); ok {
		return de
	}
	msg := err.Error()
	if strings.Contains(msg, "no authenticated identity") {
		return &diary.Error{Code: 401, Message: "unauthenticated"}
	}
	return err
}
