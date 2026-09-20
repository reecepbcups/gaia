# Builds DOOM into the wasm32-wasi reactor module that x/doom/engine embeds.
# See ../README.md.
#
# doomgeneric.tar.gz is upstream doomgeneric, kept as one blob so 178 files of
# vendored C stay out of the diff. Only doomgeneric_chain.c, the port that makes
# the host drive the game, lives here in the open.
#
# Needs wasi-sdk: `make toolchain` puts one in ~/.wasi-sdk.

WASI_SDK ?= $(HOME)/.wasi-sdk
WASI_SDK_VERSION ?= 34
CC := $(WASI_SDK)/bin/clang

OUT := ../engine/doom.wasm
SRCDIR := build/src
OBJDIR := build/obj
STAMP := build/.extracted

UNAME_S := $(shell uname -s | tr A-Z a-z)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),darwin)
	SDK_ARCH := $(UNAME_M)-macos
else
	SDK_ARCH := $(UNAME_M)-linux
endif
SDK_URL := https://github.com/WebAssembly/wasi-sdk/releases/download/wasi-sdk-$(WASI_SDK_VERSION)/wasi-sdk-$(WASI_SDK_VERSION).0-$(SDK_ARCH).tar.gz

# CMAP256 keeps the framebuffer 8-bit paletted: 64000 bytes a frame instead of
# 256000, which matters when every frame goes out over a stream.
CFLAGS := -Os -Wall -Wno-unused-variable -Wno-parentheses \
	-DNORMALUNIX -DLINUX -D_DEFAULT_SOURCE \
	-DCMAP256 -DDOOMGENERIC_RESX=320 -DDOOMGENERIC_RESY=200

LDFLAGS := -mexec-model=reactor -Wl,--max-memory=67108864

.PHONY: all compile clean toolchain

# Two passes: the source list can only be globbed once the tarball is unpacked.
all: $(STAMP)
	@$(MAKE) --no-print-directory compile

$(STAMP): doomgeneric.tar.gz doomgeneric_chain.c
	@echo "unpacking doomgeneric"
	@rm -rf $(SRCDIR)
	@mkdir -p $(SRCDIR) $(OBJDIR)
	@tar xzf doomgeneric.tar.gz -C $(SRCDIR)
	@cp doomgeneric_chain.c $(SRCDIR)/
	@touch $@

SRCS := $(wildcard $(SRCDIR)/*.c)
OBJS := $(patsubst $(SRCDIR)/%.c,$(OBJDIR)/%.o,$(SRCS))

compile: $(OUT)

$(OUT): $(OBJS)
	@mkdir -p $(dir $@)
	$(CC) $(CFLAGS) $(LDFLAGS) $(OBJS) -o $@
	@ls -l $@

$(OBJDIR)/%.o: $(SRCDIR)/%.c
	@$(CC) $(CFLAGS) -c $< -o $@

toolchain:
	@test -x $(CC) || ( \
		echo "fetching wasi-sdk-$(WASI_SDK_VERSION) for $(SDK_ARCH)" && \
		mkdir -p $(WASI_SDK) && \
		curl -sL $(SDK_URL) | tar xz -C $(WASI_SDK) --strip-components=1 )
	@$(CC) --version | head -1

clean:
	rm -rf build
