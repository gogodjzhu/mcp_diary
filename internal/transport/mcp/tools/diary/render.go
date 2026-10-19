package diary

import (
	"context"

	corediary "github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

// newRenderDiaryEntries is a presentation tool: it runs the same query as
// listDiaryEntries and returns the same page shape, but the registry tags it
// with the UI view (see ui.ToolMetas) so the host renders the entries as an
// interactive card list.
//
// Keeping it separate from listDiaryEntries means the data tool stays free of
// UI: the model can read entries without remounting a widget, and clients
// without MCP Apps simply get JSON.
func newRenderDiaryEntries(workspaces tools.WorkspaceProvider, svc *corediary.Service) tools.Tool {
	return diaryTool{
		name: "renderDiaryEntries",
		definition: mcp.NewTool(
			"renderDiaryEntries",
			mcp.WithDescription("Show the user an interactive card list of committed diary entries. Use when the user asks to see, browse or review their diary. Returns the same page as listDiaryEntries. Clients without UI support show JSON instead."),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithToolTitle("Show diary entries"),
			mcp.WithString("request_id", mcp.Required(), mcp.Description("Caller-generated idempotency key.")),
			mcp.WithString("diary_date_from", mcp.Description("Inclusive start date YYYY-MM-DD.")),
			mcp.WithString("diary_date_to", mcp.Description("Inclusive end date YYYY-MM-DD.")),
			mcp.WithInteger("page", mcp.Description("1-based page number. Defaults to 1.")),
			mcp.WithInteger("page_size", mcp.Description("Page size. Defaults to 20, max 100.")),
		),
		handle: func(ctx context.Context, request mcp.CallToolRequest) (any, error) {
			fs, err := workspaceFS(ctx, workspaces)
			if err != nil {
				return nil, err
			}
			requestID, err := requireRequestID(request)
			if err != nil {
				return nil, err
			}
			return svc.ListEntries(ctx, fs, corediary.ListEntriesIn{
				RequestID:     requestID,
				DiaryDateFrom: optionalString(request, "diary_date_from"),
				DiaryDateTo:   optionalString(request, "diary_date_to"),
				Page:          request.GetInt("page", 1),
				PageSize:      request.GetInt("page_size", 20),
			})
		},
	}
}
