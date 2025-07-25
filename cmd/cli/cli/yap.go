// Package cli provides easy-to-use commands to manage, monitor, and utilize AIS clusters.
/*
 * Copyright (c) 2018-2025, NVIDIA CORPORATION. All rights reserved.
 */
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/aistore/api"
	"github.com/NVIDIA/aistore/api/apc"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/cos"

	"github.com/urfave/cli"
)

// generic types
type (
	here struct {
		arg     string
		abspath string
		tmpl    string
		finfo   os.FileInfo
		fdnames []string // files and directories (names)
		isdir   bool
		recurs  bool
		stdin   bool
	}
	there struct {
		bck   cmn.Bck
		oname string
		lr    apc.ListRange
	}
)

// assorted specific
type (
	wop interface {
		verb() string
		dest() string
	}
	// PUT object(s)
	putargs struct {
		src here
		pt  *cos.ParsedTemplate // client-side, via --list|template or src/range
		dst there
	}
	// 'ais archive bucket'
	archbck struct {
		putargs
		rsrc        there
		apndIfExist bool
	}
	// 'ais archive put' (with an option to append)
	archput struct {
		putargs
		archpath    string
		appendOnly  bool
		appendOrPut bool
	}
)

// interface guard
var (
	_ wop = (*putargs)(nil)
	_ wop = (*archbck)(nil)
	_ wop = (*archput)(nil)
)

func (*putargs) verb() string { return "PUT" }

func (a *putargs) dest() string {
	if a.dst.oname == "" {
		return a.dst.bck.Cname("")
	}
	// if len(a.src.fdnames) < 2 {
	return a.dst.bck.Cname(a.dst.oname)
}

func (a *putargs) srcIsRegular() bool { return a.src.finfo != nil && !a.src.isdir }

func (a *putargs) parse(c *cli.Context, emptyDstOnameOK bool) (err error) {
	if c.NArg() == 0 {
		return missingArgumentsError(c, c.Command.ArgsUsage)
	}
	if err := errMutuallyExclusive(c, listFlag, templateFlag); err != nil {
		return err
	}
	if flagIsSet(c, progressFlag) || flagIsSet(c, listFlag) || flagIsSet(c, templateFlag) {
		// check connectivity (since '--progress' steals STDOUT with multi-object producing
		// scary looking errors when there's no cluster)
		if _, err := api.GetClusterMap(apiBP); err != nil {
			return err
		}
	}
	switch {
	case c.NArg() == 1: // BUCKET/[OBJECT_NAME] --list|--template
		uri := c.Args().Get(0) // dst
		a.dst.bck, a.dst.oname, err = parseBckObjURI(c, uri, emptyDstOnameOK)
		if err != nil {
			return err
		}

		// source files via '--list' or '--template'
		switch {
		case flagIsSet(c, listFlag):
			csv := parseStrFlag(c, listFlag)
			a.src.fdnames = splitCsv(csv)
			return nil
		case flagIsSet(c, templateFlag):
			a.src.tmpl = parseStrFlag(c, templateFlag)
			pt, err := cos.NewParsedTemplate(a.src.tmpl)
			switch err {
			case nil:
				a.pt = &pt
			case cos.ErrEmptyTemplate:
				err = errors.New("template to select source files cannot be empty")
			}
			return err
		default:
			return fmt.Errorf("missing source arg in %q", c.Command.ArgsUsage)
		}

	case c.NArg() == 2: // FILE|DIRECTORY|DIRECTORY/PATTERN   BUCKET/[OBJECT_NAME]
		a.src.arg = c.Args().Get(0) // src
		uri := c.Args().Get(1)      // dst

		a.dst.bck, a.dst.oname, err = parseBckObjURI(c, uri, emptyDstOnameOK)
		if err != nil {
			return err
		}

		const efmt = "source (%q) and flag (%s) are mutually exclusive"
		if flagIsSet(c, listFlag) {
			return fmt.Errorf(efmt, a.src.arg, qflprn(listFlag))
		}
		if flagIsSet(c, templateFlag) {
			return fmt.Errorf(efmt, a.src.arg, qflprn(templateFlag))
		}

		// STDIN
		if a.src.arg == "-" {
			a.src.stdin = true
			if a.dst.oname == "" {
				err = fmt.Errorf("missing destination object name (in %s) - required when writing directly from standard input",
					c.Command.ArgsUsage)
			}
			return err
		}
		// file or files
		if a.src.abspath, err = absPath(a.src.arg); err != nil {
			return err
		}

		// best-effort parsing: (inline range) | (local file or directory)

		// inline "range" w/ no flag, e.g.: "/tmp/www/test{0..2}{0..2}.txt" ais://nnn/www
		pt, e1 := cos.ParseBashTemplate(a.src.abspath)
		if e1 == nil {
			a.pt = &pt
			return nil
		}
		// local file or dir?
		finfo, e2 := os.Stat(a.src.abspath)
		if e2 != nil {
			// must be a csv list of files embedded with the first arg
			a.src.fdnames = splitCsv(a.src.arg)
			return nil
		}

		a.src.finfo = finfo
		// reg file
		if !finfo.IsDir() {
			if a.dst.oname == "" {
				// PUT [convention]: use `basename` as the destination object name, unless specified
				a.dst.oname = filepath.Base(a.src.abspath)
			}
			return nil
		}

		// finally: a local (or rather, client-accessible) directory
		a.src.isdir = true
		a.src.recurs = flagIsSet(c, recursFlag)
		return nil
	}

	if err := errTailArgsContainFlag(c.Args()[2:]); err != nil {
		return err
	}

	hint := fmt.Sprintf("(hint: wildcards must be in single or double quotes, see %s for details)", qflprn(cli.HelpFlag))
	return fmt.Errorf("too many arguments: '%s'\n"+hint, strings.Join(c.Args(), " "))
}

func (*archbck) verb() string { return "ARCHIVE" }

func (a *archbck) dest() string { return a.dst.bck.Cname(a.dst.oname) }

func (a *archbck) parse(c *cli.Context) (err error) {
	if c.NArg() == 1 {
		err = a.putargs.parse(c, false /*empty dst oname ok*/)
		if err != nil {
			return err
		}
	} else {
		uri := c.Args().Get(1) // dst
		a.dst.bck, a.dst.oname, err = parseBckObjURI(c, uri, false)
		if err != nil {
			return err
		}
	}

	// validate
	if err := errMutuallyExclusive(c, listFlag, templateFlag, verbObjPrefixFlag); err != nil {
		return err
	}

	// source bucket[/obj-or-range]
	var (
		objNameOrTmpl string
		uri           = c.Args().Get(0)
	)
	if a.rsrc.bck, objNameOrTmpl, err = parseBckObjURI(c, uri, true /*emptyObjnameOK*/); err != nil {
		return err
	}

	oltp, errV := dopOLTP(c, a.rsrc.bck, objNameOrTmpl)
	if errV != nil {
		return errV
	}

	if oltp.objName == "" && oltp.list == "" && oltp.tmpl == "" {
		a.rsrc.lr.Template = cos.WildcardMatchAll
	}
	if oltp.list == "" && oltp.tmpl == "" {
		if flagIsSet(c, nonRecursFlag) {
			oltp.tmpl = oltp.objName
		} else {
			oltp.list = oltp.objName
		}
	}
	if oltp.list != "" {
		a.rsrc.lr.ObjNames = splitCsv(oltp.list)
	} else {
		a.rsrc.lr.Template = oltp.tmpl
	}

	return nil
}

func (*archput) verb() string { return "APPEND" }

func (a *archput) dest() string { return a.dst.bck.Cname(a.dst.oname) }

func (a *archput) parse(c *cli.Context) (err error) {
	a.archpath = parseStrFlag(c, archpathFlag)
	a.appendOnly = flagIsSet(c, archAppendOnlyFlag)
	a.appendOrPut = flagIsSet(c, archAppendOrPutFlag)
	if a.appendOnly && a.appendOrPut {
		return incorrectUsageMsg(c, errFmtExclusive, qflprn(archAppendOnlyFlag), qflprn(archAppendOrPutFlag))
	}
	err = a.putargs.parse(c, false /*empty dst oname ok*/)
	return
}
