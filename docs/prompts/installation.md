# Documentation Generation Task: Installation

You are an expert technical writer. Document the installation process for `grove-hooks`.
- Check the `.github/workflows/release.yml` file to understand how binaries are built for different platforms (Linux, macOS; amd64, arm64).
- Explain that `grove-hooks` is distributed as a standalone binary.
- Provide instructions for downloading the correct binary from the project's GitHub Releases page.
- Include a step on how to make the binary executable (`chmod +x`) and move it to a directory in the user's `$PATH` (e.g., `/usr/local/bin`).
- Mention that it is part of the Grove ecosystem and may be managed by the `grove` meta-tool in the future, but for now, manual installation is the standard method.