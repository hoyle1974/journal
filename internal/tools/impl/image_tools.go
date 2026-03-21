package impl

import (
	"context"
	"fmt"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/tools"
)

// imageSentinelPrefix is embedded in the FOH answer to signal the Telegram handler to send a photo.
// Format: [SEND_IMAGE:<entry_uuid>]
const imageSentinelPrefix = "[SEND_IMAGE:"

// ImageSentinel returns the sentinel string for a given entry UUID.
func ImageSentinel(entryUUID string) string {
	return fmt.Sprintf("%s%s]", imageSentinelPrefix, entryUUID)
}

type retrieveImageArgs struct {
	EntryUUID string `json:"entry_uuid" description:"UUID of the journal entry whose image should be sent back to the user." required:"true"`
}

func init() {
	registerImageTools()
}

func registerImageTools() {
	tools.Register(&tools.Tool{
		Name:        "retrieve_image",
		Description: "Send a previously ingested image back to the user. Call this after identifying the journal entry UUID (via search_entries, get_recent_entries, or get_entries_by_date_range with has_image=true). Include the exact token returned in your final answer — the system uses it to deliver the image. Do not paraphrase or omit the token.",
		Category:    "journal",
		Args:        &retrieveImageArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*retrieveImageArgs)
			if a.EntryUUID == "" {
				return tools.MissingParam("entry_uuid")
			}
			entry, err := env.MemoryStore().GetEntry(ctx, a.EntryUUID)
			if err != nil {
				return tools.Fail("Could not find entry %q: %v", a.EntryUUID, err)
			}
			if entry.ImageURL == "" {
				return tools.Fail("Entry %q does not have an attached image.", a.EntryUUID)
			}
			desc := entry.ParsedImageDescription
			if desc == "" {
				desc = "(no description available)"
			}
			ts := memory.TruncateTimestamp(entry.Timestamp, memory.DateTimeDisplayLen)
			sentinel := ImageSentinel(a.EntryUUID)
			return tools.OK("Image found from %s. Description: %s\n\nInclude this token verbatim in your answer to deliver the image: %s", ts, desc, sentinel)
		},
	})
}
