#include <errno.h>
#include <stdlib.h>
#include <unistd.h>

#include "decode.c"

#define SRC_BUF_SIZE (64 * 1024 * 1024)
#define DST_BUF_SIZE (64 * 1024 * 1024)

uint8_t src_buf[SRC_BUF_SIZE] = {0};
size_t src_len = 0;
uint8_t dst_buf[DST_BUF_SIZE] = {0};
size_t dst_len = 0;

uint8_t* print_buf = NULL;
size_t print_len = 0;

static void ignore_return_value(int ignored) {}

const char* read_stdin() {
	while (src_len < SRC_BUF_SIZE) {
		const int stdin_fd = 0;
		ssize_t n = read(stdin_fd, src_buf + src_len, SRC_BUF_SIZE - src_len);
		if (n > 0) {
			src_len += n;
		} else if (n == 0) {
			return NULL;
		} else if (errno == EINTR) {
			// no-op
		} else {
			return strerror(errno);
		}
	}
	return "input is too large";
}

const char* decode() {
	wuffs_base__io_buffer src = {.ptr = src_buf, .len = src_len, .wi = src_len, .closed = true};
	wuffs_base__io_reader src_reader = wuffs_base__io_buffer__reader(&src);

	wuffs_base__io_buffer dst = {.ptr = dst_buf, .len = DST_BUF_SIZE};
	wuffs_base__io_writer dst_writer = wuffs_base__io_buffer__writer(&dst);

	wuffs_zstd__decoder dec = ((wuffs_zstd__decoder){});
	wuffs_zstd__decoder__check_wuffs_version(&dec, sizeof dec, WUFFS_VERSION);

	wuffs_zstd__status s = wuffs_zstd__decoder__decode(&dec, dst_writer, src_reader);
	if (s) {
		return wuffs_zstd__status__string(s);
	}
	ignore_return_value(write(1, dst.ptr, dst.wi));
	return NULL;
}

int fail(const char* msg) {
	const int stderr_fd = 2;
	write(stderr_fd, msg, strnlen(msg, 4095));
	write(stderr_fd, "\n", 1);
	return 1;
}

int main(int argc, char** argv) {
	const char* msg = read_stdin();
	if (msg) {
		return fail(msg);
	}
	msg = decode();
	if (msg) {
		return fail(msg);
	}
	return 0;
}
