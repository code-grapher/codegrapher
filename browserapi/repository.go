package browserapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/specscore/codegrapher/freshness"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
)

const repositoryIdentityFile = "browser-repository.json"

type repositoryIdentity struct {
	ID string `json:"id"`
}

func loadOrCreateRepositoryID(root string) (string, error) {
	path := filepath.Join(indexer.GetCodeGraphDir(root), repositoryIdentityFile)
	read := func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		var identity repositoryIdentity
		if err := json.Unmarshal(data, &identity); err != nil || !validRepositoryID(identity.ID) {
			return "", errors.New("invalid persisted browser repository identity")
		}
		return identity.ID, nil
	}
	if id, err := read(); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate browser repository identity: %w", err)
	}
	id := "repo_" + hex.EncodeToString(bytes)
	data, _ := json.Marshal(repositoryIdentity{ID: id})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return read()
	}
	if err != nil {
		return "", fmt.Errorf("persist browser repository identity: %w", err)
	}
	name := file.Name()
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("persist browser repository identity: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("persist browser repository identity: %w", err)
	}
	return id, nil
}

func validRepositoryID(id string) bool {
	if len(id) != len("repo_")+32 || !strings.HasPrefix(id, "repo_") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "repo_"))
	return err == nil
}

// GenerateCredential returns a cryptographically random browser bearer token.
func GenerateCredential() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate browser credential: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

type snapshot struct {
	revision                          string
	files                             map[string]model.FileRecord
	fileCount, symbolCount, edgeCount int
	indexedAt                         time.Time
}

func snapshotIndex(idx *indexer.Indexer) (snapshot, error) {
	result := snapshot{files: map[string]model.FileRecord{}}
	hash := sha256.New()
	_, _ = hash.Write([]byte("codegrapher-browser-revision-v1\x00"))
	for storeIndex, store := range idx.Stores() {
		files, err := store.GetAllFiles()
		if err != nil {
			return snapshot{}, err
		}
		sort.Slice(files, func(i, j int) bool {
			if files[i].Path != files[j].Path {
				return files[i].Path < files[j].Path
			}
			if files[i].ContentHash != files[j].ContentHash {
				return files[i].ContentHash < files[j].ContentHash
			}
			return files[i].IndexedAt < files[j].IndexedAt
		})
		stats, err := store.GetStats()
		if err != nil {
			return snapshot{}, err
		}
		metadata, err := store.GetAllMetadata()
		if err != nil {
			return snapshot{}, err
		}
		_, _ = fmt.Fprintf(hash, "store:%d\x00nodes:%d\x00edges:%d\x00", storeIndex, stats.NodeCount, stats.EdgeCount)
		keys := make([]string, 0, len(metadata))
		for key := range metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, _ = fmt.Fprintf(hash, "meta:%s=%s\x00", key, metadata[key])
		}
		result.symbolCount += stats.NodeCount
		result.edgeCount += stats.EdgeCount
		for _, file := range files {
			if _, err := normalizeRelativePath(file.Path, false); err != nil {
				return snapshot{}, errors.New("index contains an unsafe file path")
			}
			_, _ = fmt.Fprintf(hash, "file:%s\x00%s\x00%d\x00%s\x00", file.Path, file.ContentHash, file.Size, file.Language)
			if existing, ok := result.files[file.Path]; !ok || existing.IndexedAt < file.IndexedAt {
				result.files[file.Path] = file
			}
			if file.IndexedAt > result.indexedAt.UnixMilli() {
				result.indexedAt = time.UnixMilli(file.IndexedAt).UTC()
			}
		}
	}
	result.fileCount = len(result.files)
	result.revision = "rev_" + hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func publicFreshness(status freshness.Status, indexedAt time.Time) Freshness {
	state := "stale"
	if status.IndexCurrent {
		state = "ready"
	} else if status.WatchReady && status.LastError == "" {
		state = "updating"
	}
	result := Freshness{State: state}
	if !indexedAt.IsZero() {
		value := indexedAt.UTC()
		result.IndexedAt = &value
	}
	if !status.LastSuccessfulUpdate.IsZero() {
		value := status.LastSuccessfulUpdate.UTC()
		result.LastSuccessfulSync = &value
	}
	if status.LastError != "" {
		result.LastError = "index synchronization failed"
	}
	return result
}

func gitMetadata(root string) (remote, branch, head string) {
	output := func(args ...string) string {
		data, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	head = output("rev-parse", "HEAD")
	branch = output("branch", "--show-current")
	remote = sanitizeRemote(output("remote", "get-url", "origin"))
	return
}

func sanitizeRemote(raw string) string {
	if raw == "" || filepath.IsAbs(raw) || strings.HasPrefix(raw, "file:") {
		return ""
	}
	if strings.Contains(raw, "@") && !strings.Contains(raw, "://") {
		parts := strings.SplitN(raw, ":", 2)
		host := parts[0]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if len(parts) == 2 && host != "" {
			return strings.TrimSuffix(host+"/"+parts[1], ".git")
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh") {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimSuffix(parsed.String(), ".git")
}
