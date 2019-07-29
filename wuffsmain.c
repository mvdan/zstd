#include <errno.h>
#include <unistd.h>

#include "decode.h"

#ifndef DST_BUFFER_SIZE
#define DST_BUFFER_SIZE (128 * 1024)
#endif

#ifndef SRC_BUFFER_SIZE
#define SRC_BUFFER_SIZE (128 * 1024)
#endif

uint8_t dst_buffer[DST_BUFFER_SIZE];
uint8_t src_buffer[SRC_BUFFER_SIZE];

// ignore_return_value suppresses errors from -Wall -Werror.
static void ignore_return_value(int ignored) {}

static const char* decode() {
  wuffs_zstd__decoder dec;
  const char* status =
      wuffs_zstd__decoder__initialize(&dec, sizeof dec, WUFFS_VERSION, 0);
  if (status) {
    return status;
  }

  wuffs_base__io_buffer dst;
  dst.data.ptr = dst_buffer;
  dst.data.len = DST_BUFFER_SIZE;
  dst.meta.wi = 0;
  dst.meta.ri = 0;
  dst.meta.pos = 0;
  dst.meta.closed = false;

  wuffs_base__io_buffer src;
  src.data.ptr = src_buffer;
  src.data.len = SRC_BUFFER_SIZE;
  src.meta.wi = 0;
  src.meta.ri = 0;
  src.meta.pos = 0;
  src.meta.closed = false;

  while (true) {
    const int stdin_fd = 0;
    ssize_t n =
        read(stdin_fd, src.data.ptr + src.meta.wi, src.data.len - src.meta.wi);
    if (n < 0) {
      if (errno != EINTR) {
        return strerror(errno);
      }
      continue;
    }
    src.meta.wi += n;
    if (n == 0) {
      src.meta.closed = true;
    }

    while (true) {
      status = wuffs_zstd__decoder__decode(
          &dec, &dst, &src);

      if (dst.meta.wi) {
        // TODO: handle EINTR and other write errors; see "man 2 write".
        const int stdout_fd = 1;
        ignore_return_value(write(stdout_fd, dst_buffer, dst.meta.wi));
        dst.meta.ri = dst.meta.wi;
        wuffs_base__io_buffer__compact(&dst);
      }

      if (status == wuffs_base__suspension__short_read) {
        break;
      }
      if (status == wuffs_base__suspension__short_write) {
        continue;
      }
      return status;
    }

    wuffs_base__io_buffer__compact(&src);
    if (src.meta.wi == src.data.len) {
      return "internal error: no I/O progress possible";
    }
  }
}

int fail(const char* msg) {
  const int stderr_fd = 2;
  ignore_return_value(write(stderr_fd, msg, strnlen(msg, 4095)));
  ignore_return_value(write(stderr_fd, "\n", 1));
  return 1;
}

int main(int argc, char** argv) {
  const char* status = decode();
  int status_code = status ? fail(status) : 0;

  return status_code;
}
