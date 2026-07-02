# Contributing to Cyclestone

First off, thank you for considering contributing to Cyclestone! It's people like you who make open-source software a wonderful place to learn, inspire, and create.

Please review the guidelines below to ensure a smooth contribution process.

---

## 🛠️ Local Development Setup

To get started with local development:

1. **Prerequisites**: Ensure you have [Go](https://go.dev/) (version 1.24 or later) and `git` installed.
2. **Clone the Repository**:
   ```bash
   git clone https://github.com/patrick-folster/cyclestone.git
   cd cyclestone
   ```
3. **Build the Binary**:
   Verify that you can build the application locally:
   ```bash
   go build -o cyclestone ./cmd/cyclestone
   ```

---

## 🧪 Testing & Code Quality

Before submitting your changes, make sure all tests pass and code is formatted correctly:

1. **Run Unit Tests**:
   ```bash
   go test ./...
   ```
2. **Format Code**:
   Always format your code according to standard Go conventions:
   ```bash
   go fmt ./...
   ```
3. **Run Vet Check**:
   ```bash
   go vet ./...
   ```

---

## 🚀 How to Submit a Pull Request

1. **Create a Branch**: Fork the repo and create your branch from `main`. Use a descriptive name (e.g., `feat/custom-milestone-path` or `fix/git-diff-parser`).
2. **Implement Your Changes**:
   - Write clean, documented Go code.
   - Keep existing comments and docstrings intact unless they are directly affected by your change.
   - Add unit tests for new behavior where possible.
3. **Commit Your Changes**: Keep your commit messages clear and concise.
4. **Push & Open PR**: Push to your fork and submit a Pull Request. Provide a clear explanation of what your PR changes, why, and how it was tested.

Thank you for your contribution!
