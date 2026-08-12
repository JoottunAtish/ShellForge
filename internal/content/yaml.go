package content

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// Unmarshal decodes YAML data into v, naming filename in any error so the
// learner or the level author can find the file that failed to parse.
//
// This is the decoding primitive the level loader and validator build on. It
// carries no knowledge of the level schema: naming the level id and the field
// in error text, as docs/LEVEL-FORMAT.md requires of the validator, is that
// validator's own job, tracked in #53. Strict mode is on, so an unrecognized
// field is reported here rather than silently ignored, which is what lets a
// typo in a level's YAML be a load error instead of a level that quietly does
// the wrong thing.
func Unmarshal(filename string, data []byte, v any) error {
	if err := yaml.UnmarshalWithOptions(data, v, yaml.Strict()); err != nil {
		return fmt.Errorf("parse %s: %w", filename, err)
	}
	return nil
}
