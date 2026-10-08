package ui

import (
	"github.com/gboyke/lalabase-cli/internal/present"
	"github.com/gboyke/lalabase-cli/internal/uitext"
)

// presentation is the one mapping from content to display records. Future
// API and cache readers must take this same path, including cache hits.
func presentation(organisation, list, object string) present.Shell {
	return present.Shell{Organisation: present.NewInline(organisation), List: present.NewBody(list), Object: present.NewBody(object)}
}

func placeholder() present.Shell {
	return presentation(uitext.Inline(uitext.NotLoaded).String(), uitext.Body(uitext.NoData).String(), uitext.Body(uitext.NoItem).String())
}
