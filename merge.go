package zoekt

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

func Merge(dstDir string, files ...IndexFile) error {
	var ds []*indexData
	for _, f := range files {
		searcher, err := NewSearcher(f)
		if err != nil {
			return err
		}
		ds = append(ds, searcher.(*indexData))
	}

	ib, err := merge(ds...)
	if err != nil {
		return err
	}

	fn := filepath.Join(dstDir, fmt.Sprintf("merged_v%d.%05d.zoekt", IndexFormatVersion, 0))
	return builderWriteAll(fn, ib)
}

func builderWriteAll(fn string, ib *IndexBuilder) error {
	dir := filepath.Dir(fn)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := ioutil.TempFile(dir, filepath.Base(fn)+".*.tmp")
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		// umask?
		if err := f.Chmod(0o666); err != nil {
			return err
		}
	}

	defer f.Close()
	if err := ib.Write(f); err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(f.Name(), fn); err != nil {
		return err
	}

	log.Printf("finished %s: %d index bytes (overhead %3.1f)", fn, fi.Size(),
		float64(fi.Size())/float64(ib.ContentSize()+1))

	return nil
}

func merge(ds ...*indexData) (*IndexBuilder, error) {
	if len(ds) == 0 {
		return nil, fmt.Errorf("need 1 or more indexData to merge")
	}

	ib, err := NewIndexBuilder(&ds[0].Repository()[0])
	if err != nil {
		return nil, err
	}

	lastRepoID := 0

	for _, d := range ds {
		for docID := uint32(0); int(docID) < len(d.fileBranchMasks); docID++ {
			repoID := int(d.repos[docID])

			if repoID != lastRepoID {
				if lastRepoID+1 != repoID {
					return nil, fmt.Errorf("non-contiguous repo ids in %s for document %d: old=%d current=%d", d.String(), docID, lastRepoID, repoID)
				}
				ib.setRepository(&d.repoMetaData[repoID])
				lastRepoID = repoID
			}

			doc := Document{
				Name: string(d.fileName(docID)),
				// Content set below since it can return an error
				// Branches set below since it requires lookups
				SubRepositoryPath: d.subRepoPaths[repoID][d.subRepos[docID]],
				Language:          d.languageMap[d.languages[docID]],
				// SkipReason not set, will be part of content from original indexer.
			}

			if doc.Content, err = d.readContents(docID); err != nil {
				return nil, err
			}

			if doc.Symbols, _, err = d.readDocSections(docID, nil); err != nil {
				return nil, err
			}

			doc.SymbolsMetaData = make([]*Symbol, len(doc.Symbols))
			for i := range doc.SymbolsMetaData {
				doc.SymbolsMetaData[i] = d.symbols.data(d.fileEndSymbol[docID] + uint32(i))
			}

			// calculate branches
			{
				mask := d.fileBranchMasks[docID]
				id := uint32(1)
				for mask != 0 {
					if mask&0x1 != 0 {
						doc.Branches = append(doc.Branches, d.branchNames[repoID][uint(id)])
					}
					id <<= 1
					mask >>= 1
				}
			}

			if err := ib.Add(doc); err != nil {
				return nil, err
			}
		}

		// reset lastRepoID so on the next loop we call setRepository.
		lastRepoID = -1
	}

	return ib, nil
}
