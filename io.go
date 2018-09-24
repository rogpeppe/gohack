package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/errgo.v2/fmt/errors"
)

func isEmptyDir(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, errors.Wrap(err)
	}
	defer f.Close()
	_, err = f.Readdir(1)
	if err != nil && err != io.EOF {
		return false, errors.Wrap(err)
	}
	return err == io.EOF, nil
}

func copyAll(dst, src string) error {
	srcInfo, srcErr := os.Lstat(src)
	if srcErr != nil {
		return errors.Wrap(srcErr)
	}
	_, dstErr := os.Lstat(dst)
	if dstErr == nil {
		return errors.Newf("will not overwrite %q", dst)
	}
	if !os.IsNotExist(dstErr) {
		return errors.Wrap(dstErr)
	}
	switch mode := srcInfo.Mode(); mode & os.ModeType {
	case os.ModeSymlink:
		return errors.Newf("will not copy symbolic link")
	case os.ModeDir:
		return copyDir(dst, src)
	case 0:
		return copyFile(dst, src)
	default:
		return fmt.Errorf("cannot copy file with mode %v", mode)
	}
}

func copyFile(dst, src string) error {
	srcf, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err)
	}
	defer srcf.Close()
	dstf, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return errors.Wrap(err)
	}
	defer dstf.Close()
	if _, err := io.Copy(dstf, srcf); err != nil {
		return fmt.Errorf("cannot copy %q to %q: %v", src, dst, err)
	}
	return nil
}

func copyDir(dst, src string) error {
	srcf, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err)
	}
	defer srcf.Close()
	if err := os.Mkdir(dst, 0777); err != nil {
		return errors.Wrap(err)
	}
	for {
		names, err := srcf.Readdirnames(100)
		for _, name := range names {
			if err := copyAll(filepath.Join(dst, name), filepath.Join(src, name)); err != nil {
				return errors.Wrap(err)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Newf("error reading directory %q: %v", src, err)
		}
	}
	return nil
}
