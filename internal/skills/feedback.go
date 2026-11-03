package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
)

type Feedback struct {
	Name      string `json:"name"`
	Rating    int    `json:"rating"`
	Comment   string `json:"comment,omitempty"`
	CreatedAt string `json:"created_at"`
	Path      string `json:"path"`
}

func RecordFeedback(targetPath, name string, rating int, comment string) (Feedback, error) {
	name = oneLine(name)
	if name == "" {
		return Feedback{}, fmt.Errorf("skill name is required")
	}
	if rating < 1 || rating > 5 {
		return Feedback{}, fmt.Errorf("rating must be between 1 and 5")
	}
	if strings.TrimSpace(targetPath) == "" {
		path, err := config.CurrentIdentity("").GlobalStatePath("skill-feedback.jsonl")
		if err != nil {
			return Feedback{}, err
		}
		targetPath = path
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return Feedback{}, err
	}
	feedback := Feedback{
		Name:      name,
		Rating:    rating,
		Comment:   strings.TrimSpace(comment),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Path:      targetPath,
	}
	data, err := json.Marshal(feedback)
	if err != nil {
		return Feedback{}, err
	}
	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return Feedback{}, err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return Feedback{}, err
	}
	return feedback, nil
}
