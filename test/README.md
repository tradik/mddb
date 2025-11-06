# MDDB Performance Tests

This directory contains performance testing scripts for MDDB, including HTTP vs gRPC protocol comparison.

## Files

- `generate-lorem.sh` - Generates Lorem Ipsum markdown files in 3 sizes
- `performance-test.sh` - HTTP/JSON performance test script
- `grpc-performance-test.go` - gRPC/Protobuf performance test
- `compare-protocols.sh` - Compare HTTP vs gRPC performance
- `lorem-*.md` - Generated test documents (3 sizes)
- `performance-results-*.txt` - Test results (generated)
- `grpc-performance-results.txt` - gRPC test results
- `protocol-comparison-*.txt` - HTTP vs gRPC comparison

## Quick Start

```bash
# Make sure MDDB server is running
make docker-up-dev

# Run HTTP performance test
cd test
./performance-test.sh

# Run gRPC performance test
go run grpc-performance-test.go

# Compare both protocols
./compare-protocols.sh
```

## What it Tests

The script performs 3 test runs:

1. **Short Documents** (~124 bytes) - 10,000 documents
2. **Medium Documents** (~707 bytes) - 10,000 documents  
3. **Long Documents** (~1,876 bytes) - 10,000 documents

**Total: 30,000 documents**

## Metrics Measured

For each test:
- **Average time** per document insertion
- **Min/Max time** for single insertion
- **Median time** for insertions
- **Throughput** (documents per second)
- **Total time** for all insertions

## Configuration

You can customize the test by setting environment variables:

```bash
# Change number of documents (default: 10000)
TOTAL_DOCS=5000 ./performance-test.sh

# Use different server
MDDB_SERVER=http://localhost:8080 ./performance-test.sh

# Use different CLI binary
MDDB_CLI=/usr/local/bin/mddb-cli ./performance-test.sh
```

## Example Output

```
════════════════════════════════════════════════
  MDDB Performance Test
════════════════════════════════════════════════

Checking server connectivity...
✓ Server is running

Test Configuration:
  Collection: perftest
  Total documents: 10000
  Batch size: 100
  Server: http://localhost:11023

Document sizes:
  Short:  124 bytes
  Medium: 707 bytes
  Long:   1876 bytes

════════════════════════════════════════════════
  Starting Performance Test
════════════════════════════════════════════════

Test 1: Adding 10000 short documents
Progress: 
  [##################################################] 100% (10000/10000 docs, avg: 15ms)
✓ Completed

Test 2: Adding 10000 medium documents
Progress: 
  [##################################################] 100% (10000/10000 docs, avg: 18ms)
✓ Completed

Test 3: Adding 10000 long documents
Progress: 
  [##################################################] 100% (10000/10000 docs, avg: 22ms)
✓ Completed

════════════════════════════════════════════════
  Performance Test Results
════════════════════════════════════════════════

Short Documents (124 bytes):
  Documents:      10000
  Total time:     150000ms (150.00s)
  Average:        15ms per document
  Min:            8ms
  Max:            45ms
  Median:         14ms
  Throughput:     66.67 docs/sec

Medium Documents (707 bytes):
  Documents:      10000
  Total time:     180000ms (180.00s)
  Average:        18ms per document
  Min:            10ms
  Max:            52ms
  Median:         17ms
  Throughput:     55.56 docs/sec

Long Documents (1876 bytes):
  Documents:      10000
  Total time:     220000ms (220.00s)
  Average:        22ms per document
  Min:            12ms
  Max:            68ms
  Median:         21ms
  Throughput:     45.45 docs/sec

Overall Statistics:
  Total documents:    30000
  Total time:         550000ms (550.00s)
  Average per doc:    18ms
  Overall throughput: 54.55 docs/sec

Server Statistics:
  Database size:      45MB
  Total documents:    30000
  Total revisions:    30000
  Collections:        1

════════════════════════════════════════════════
  Test completed successfully!
════════════════════════════════════════════════

Results saved to: performance-results-20251106-224500.txt
```

## Results Files

Test results are automatically saved to timestamped files:
- `performance-results-YYYYMMDD-HHMMSS.txt`

These files contain detailed statistics for later analysis.

## Tips

### Faster Testing

For quick tests, reduce the document count:

```bash
# Edit performance-test.sh and change:
TOTAL_DOCS=1000  # Instead of 10000
```

### Stress Testing

For stress testing, increase the count:

```bash
# Edit performance-test.sh and change:
TOTAL_DOCS=100000  # 100k documents per size = 300k total
```

### Clean Up Test Data

MDDB doesn't have a delete endpoint yet, so test data remains in the database. To clean up:

```bash
# Stop container and remove volume
make docker-down
docker volume rm mddb-dev-data

# Restart
make docker-up-dev
```

## Troubleshooting

### Server not running

```
Error: Cannot connect to MDDB server
```

**Solution**: Start the server first:
```bash
make docker-up-dev
```

### Permission denied

```
bash: ./performance-test.sh: Permission denied
```

**Solution**: Make scripts executable:
```bash
chmod +x *.sh
```

### Out of memory

If testing with very large document counts, you may need to increase Docker memory limits.

## See Also

- [API Documentation](../docs/API.md)
- [Docker Guide](../docs/DOCKER.md)
- [Architecture](../docs/ARCHITECTURE.md)
