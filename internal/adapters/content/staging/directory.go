package staging

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"unicode/utf8"

	importer "haeryong.local/haeryong/internal/application/import"
)

// ArchiveDirectory captures an already drained generation's selected output
// tree. The caller owns process cessation and the current job/attempt authority;
// check revalidates that authority before each entry and streamed file read.
// The archive is service transport, not an author-written payload manifest.
func ArchiveDirectory(ctx context.Context, directory string, output io.Writer, check func() error) error {
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check == nil {
			return importer.ErrInputChanged
		}
		return check()
	}
	if err := guard(); err != nil {
		return err
	}
	before, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return importer.ErrInputChanged
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return errors.Join(importer.ErrInputChanged, err)
	}
	archive := zip.NewWriter(output)
	var walk func(string) error
	walk = func(name string) error {
		if err := guard(); err != nil {
			return err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if name != "." {
			if !utf8.ValidString(name) {
				return importer.PathError(name, importer.ErrInputChanged)
			}
			if err := importer.ValidatePath(name); err != nil {
				return err
			}
		}
		if info.IsDir() {
			if name != "." {
				header := &zip.FileHeader{Name: name + "/", Method: zip.Store}
				header.SetMode(os.ModeDir | 0700)
				if _, err := archive.CreateHeader(header); err != nil {
					return err
				}
			}
			dir, err := root.Open(name)
			if err != nil {
				return err
			}
			defer dir.Close()
			actual, err := dir.Stat()
			if err != nil || !sameEntry(info, actual) {
				return errors.Join(importer.ErrInputChanged, err)
			}
			for {
				children, readErr := dir.ReadDir(64)
				for _, child := range children {
					if err := walk(path.Join(name, child.Name())); err != nil {
						return err
					}
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return readErr
				}
			}
			after, err := root.Lstat(name)
			if err != nil || !sameEntry(info, after) {
				return errors.Join(importer.ErrInputChanged, err)
			}
			return nil
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := root.Readlink(name)
			if err != nil || !utf8.ValidString(link) {
				return errors.Join(importer.PathError(name, importer.ErrInputChanged), err)
			}
			header.SetMode(os.ModeSymlink | 0700)
			writer, err := archive.CreateHeader(header)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(writer, link); err != nil {
				return err
			}
			after, err := root.Lstat(name)
			current, linkErr := root.Readlink(name)
			if err != nil || linkErr != nil || !sameEntry(info, after) || link != current {
				return errors.Join(importer.ErrInputChanged, err, linkErr)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return importer.PathError(name, importer.ErrPackageInvalid)
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil || !sameEntry(info, actual) {
			return errors.Join(importer.ErrInputChanged, err)
		}
		header.SetMode(0600)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		n, err := io.Copy(writer, directoryReadGuard{file, guard})
		if err != nil {
			return err
		}
		after, statErr := file.Stat()
		current, pathErr := root.Lstat(name)
		if statErr != nil || pathErr != nil || n != info.Size() || !sameEntry(info, after) || !sameEntry(info, current) {
			return errors.Join(importer.ErrInputChanged, statErr, pathErr)
		}
		return nil
	}
	if err := walk("."); err != nil {
		return err // A failed archive is incomplete; never finalize its directory.
	}
	if err := guard(); err != nil {
		return err
	}
	return archive.Close()
}

func sameEntry(before, after os.FileInfo) bool {
	return after != nil && os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

type directoryReadGuard struct {
	reader io.Reader
	check  func() error
}

func (r directoryReadGuard) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
