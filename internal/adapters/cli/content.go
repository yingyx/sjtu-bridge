package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
)

type contentClient interface {
	DescribeContent(context.Context, string) (contracts.ContentDescriptor, error)
	DownloadContent(context.Context, string, io.Writer, func(int64, int64) error) (contracts.ContentDescriptor, error)
	Close()
}

func addContent(root *cobra.Command, out, diagnostics io.Writer, connection *string, asJSON, verbose *bool) {
	addContentWithOpen(root, out, diagnostics, connection, asJSON, verbose, func(path string) (contentClient, error) { return client.Open(path) })
}

func addContentWithOpen(root *cobra.Command, out, diagnostics io.Writer, connection *string, asJSON, verbose *bool, open func(string) (contentClient, error)) {
	group := &cobra.Command{Use: "content", GroupID: "runtime", Short: "读取能力返回的内容引用（需应用连接）", Long: "描述或下载能力返回的短期内容引用。引用不是文件路径或网址；本命令本身不提供校园文件查询能力。"}
	connect := func() (contentClient, error) {
		if *connection == "" {
			return nil, contracts.Fail("CLIENT_UNAUTHORIZED", "Content access requires --connection")
		}
		return open(*connection)
	}
	group.AddCommand(&cobra.Command{Use: "describe REF", Short: "查看内容名称、类型、大小与有效期", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		api, err := connect()
		if err != nil {
			return err
		}
		defer api.Close()
		d, err := api.DescribeContent(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return writeResult(out, diagnostics, d, *asJSON, *verbose)
	}})
	var output string
	var progress bool
	download := &cobra.Command{Use: "download REF --output PATH", Short: "分块保存内容；失败清理本次文件，不覆盖已有文件", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if output == "" {
			return contracts.Fail("INVALID_ARGUMENT", "--output is required")
		}
		api, err := connect()
		if err != nil {
			return err
		}
		defer api.Close()
		path, err := filepath.Abs(output)
		if err != nil {
			return contracts.Fail("INVALID_ARGUMENT", "Invalid output path")
		}
		d, err := saveContent(cmd.Context(), api, args[0], path, func(done, total int64) error {
			if progress {
				_, err := fmt.Fprintf(diagnostics, "下载 %d / %d 字节\n", done, total)
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		return writeResult(out, diagnostics, map[string]any{"status": "complete", "path": path, "bytes": d.Size, "content": d}, *asJSON, *verbose)
	}}
	download.Flags().StringVar(&output, "output", "", "输出文件路径；父目录须存在，文件须不存在")
	download.Flags().BoolVar(&progress, "progress", false, "向 stderr 输出分块进度")
	_ = download.MarkFlagRequired("output")
	group.AddCommand(download)
	root.AddCommand(group)
}

func saveContent(ctx context.Context, api contentClient, ref, path string, progress func(int64, int64) error) (d contracts.ContentDescriptor, err error) {
	if !contracts.ValidContentRef(ref) {
		return d, contracts.Fail("INVALID_ARGUMENT", "Invalid content reference")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return d, contracts.Fail("OUTPUT_UNAVAILABLE", "Output must be a new writable file in an existing directory")
	}
	complete := false
	defer func() {
		_ = f.Close()
		if !complete {
			if e := os.Remove(path); e != nil {
				err = contracts.Fail("OUTPUT_CLEANUP_FAILED", "Transfer failed; partial output could not be removed. Remove the requested output file manually")
			}
		}
	}()
	if local.Protect(path) != nil {
		return d, contracts.Fail("OUTPUT_UNAVAILABLE", "Cannot protect output file")
	}
	d, err = api.DownloadContent(ctx, ref, f, progress)
	if err != nil {
		return d, err
	}
	if f.Sync() != nil {
		return d, contracts.Fail("OUTPUT_UNAVAILABLE", "Cannot flush complete output")
	}
	if f.Close() != nil {
		return d, contracts.Fail("OUTPUT_UNAVAILABLE", "Cannot close complete output")
	}
	complete = true
	return d, nil
}
