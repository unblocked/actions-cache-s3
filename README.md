# S3 Cache for GitHub Actions

### Forked Changes
This is a fork of [action-s3-cache](https://github.com/try-keep/action-s3-cache).

The changes includes:
1. Adding ability to provide aws-session-token for assuming AWS IAM roles.
2. Fixing upload/download bugs (especially with larger files) and optimizing for concurrency.
3. Moving to zstd rather than pgzip.

### Archiving artifacts

```yml
- name: Save cache
  uses: try-keep/action-s3-cache@v1
  with:
    action: put
    aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
    aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    aws-region: us-east-1 # Or whatever region your bucket was created
    bucket: your-bucket
    s3-class: ONEZONE_IA # It's STANDARD by default. It can be either STANDARD,
    # REDUCED_REDUDANCY, ONEZONE_IA, INTELLIGENT_TIERING, GLACIER, DEEP_ARCHIVE or STANDARD_IA.
    key: ${{ runner.os }}-yarn-${{ hashFiles('yarn.lock') }}
    default-key: ${{ runner.os }}-yarn
    artifacts: |
      node_modules/*
```

### Retrieving artifacts

```yml
- name: Retrieve cache
  uses: try-keep/action-s3-cache@v1
  with:
    action: get
    aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
    aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    aws-region: us-east-1
    bucket: your-bucket
    key: ${{ runner.os }}-yarn-${{ hashFiles('yarn.lock') }}
    default-key: ${{ runner.os }}-yarn
```

### Clear cache

```yml
- name: Clear cache
  uses: try-keep/action-s3-cache@v1
  with:
    action: delete
    aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
    aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    aws-region: us-east-1
    bucket: your-bucket
    key: ${{ runner.os }}-yarn-${{ hashFiles('yarn.lock') }}
    default-key: ${{ runner.os }}-yarn
```

## Example

The following example shows a simple pipeline using S3 Cache GitHub Action:

```yml
- name: Checkout
  uses: actions/checkout@v2

- name: Retrieve cache
  uses: try-keep/action-s3-cache@v1
  with:
    action: get
    aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
    aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    aws-region: us-east-1
    bucket: your-bucket
    key: ${{ runner.os }}-yarn-${{ hashFiles('yarn.lock') }}
    default-key: ${{ runner.os }}-yarn

- name: Install dependencies
  run: yarn

- name: Save cache
  uses: try-keep/action-s3-cache@v1
  with:
    action: put
    aws-access-key-id: ${{ secrets.AWS_ACCESS_KEY_ID }}
    aws-secret-access-key: ${{ secrets.AWS_SECRET_ACCESS_KEY }}
    aws-region: us-east-1
    bucket: your-bucket
    s3-class: STANDARD_IA
    key: ${{ runner.os }}-yarn-${{ hashFiles('yarn.lock') }}
    default-key: ${{ runner.os }}-yarn
    artifacts: |
      node_modules/*
```

## Development

### Running Tests

This project includes comprehensive unit and integration tests for all S3 operations including upload, download, and delete functionality.

#### Unit Tests Only (No Docker Required)

Run tests that don't require S3 connectivity:

```bash
make test-unit
```

This runs tests for:
- Archive compression/decompression
- Byte formatting utilities
- Part size optimization

#### Full Integration Tests (Requires Docker)

Run all tests including S3 integration tests:

```bash
make test
```

This will:
1. Automatically start MinIO (S3-compatible storage) in Docker
2. Run all unit tests
3. Run S3 integration tests including:
   - `TestPutAndGetObject` - Upload and download operations
   - `TestStreamUpload` - Streaming upload functionality
   - `TestDeleteObject` - Delete existing objects
   - `TestDeleteNonExistentObject` - Handle deletion of non-existent objects
   - `TestDeleteObjectProperties` - Verify object properties after deletion
4. Clean up MinIO container

#### Manual Testing with MinIO

Start MinIO for manual testing:

```bash
make test-minio-up
```

MinIO console will be available at http://localhost:9001 (credentials: minioadmin/minioadmin)

Stop MinIO:

```bash
make test-minio-down
```

### Building

Build binaries for all platforms:

```bash
make build-dist
```

This creates binaries in the `dist/` directory for Linux, macOS, and Windows.
