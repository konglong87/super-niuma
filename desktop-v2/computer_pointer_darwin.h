#ifndef NIUMA_COMPUTER_POINTER_DARWIN_H
#define NIUMA_COMPUTER_POINTER_DARWIN_H

#ifdef __cplusplus
extern "C" {
#endif

typedef struct goe2e_computer_pointer goe2e_computer_pointer;

goe2e_computer_pointer *goe2e_computer_pointer_create(void);
void goe2e_computer_pointer_show(goe2e_computer_pointer *pointer, const char *json_utf8);
void goe2e_computer_pointer_hide(goe2e_computer_pointer *pointer);
void goe2e_computer_pointer_close(goe2e_computer_pointer *pointer);

// Acceptance-only probes. Runtime code must use create/show/hide/close only.
int goe2e_computer_pointer_capture_overlay(goe2e_computer_pointer *pointer, const char *path_utf8);
int goe2e_computer_pointer_geometry(goe2e_computer_pointer *pointer, double *global_x, double *global_y,
                                    double *appkit_x, double *appkit_y, double *primary_top,
                                    double *frame_x, double *frame_y, double *frame_width, double *frame_height);

#ifdef __cplusplus
}
#endif

#endif
