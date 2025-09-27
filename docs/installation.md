`grove-hooks` is distributed as a pre-compiled, standalone binary for Linux and macOS. Installation involves downloading the appropriate binary for your system, making it executable, and placing it in a directory within your system's `PATH`.

### Step 1: Download the Binary

Navigate to the project's [GitHub Releases page](https://github.com/mattsolo1/grove-hooks/releases).

Identify and download the correct binary for your operating system (OS) and architecture. The naming convention is `hooks-OS-ARCH`.

*   **macOS (Apple Silicon, M1/M2/M3):** `hooks-darwin-arm64`
*   **macOS (Intel):** `hooks-darwin-amd64`
*   **Linux (x86_64):** `hooks-linux-amd64`

You can download it through your browser or use a command-line tool like `curl`. For example, to download the binary for macOS on Apple Silicon:

```sh
# Replace <version> with the desired release version (e.g., v0.0.8)
curl -LO https://github.com/mattsolo1/grove-hooks/releases/download/<version>/hooks-darwin-arm64
```

### Step 2: Make the Binary Executable

After downloading, you must grant the binary execute permissions.

```sh
# Replace with the name of the file you downloaded
chmod +x hooks-darwin-arm64
```

### Step 3: Move to a Directory in Your PATH

Move the binary to a directory in your system's `PATH` to make it accessible from any terminal location. Renaming it to `grove-hooks` during this step simplifies its usage. A common location is `/usr/local/bin`.

```sh
# Replace with the name of the file you downloaded
# You may need to use 'sudo' depending on the permissions of the target directory
sudo mv hooks-darwin-arm64 /usr/local/bin/grove-hooks
```

### Step 4: Verify the Installation

Confirm that the binary is installed correctly by checking its version.

```sh
grove-hooks version
```

You should see output similar to this:

```
grove-hooks version v0.0.8 (commit: a1b2c3d, branch: main, built: 2025-09-17T12:00:00Z)
```

### Ecosystem Integration Note

While `grove-hooks` can be installed manually as described, it is part of the larger Grove ecosystem. In the future, installation and version management may be handled by the `grove` meta-tool. For now, the manual installation method is the standard approach.