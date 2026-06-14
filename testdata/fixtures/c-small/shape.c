/* shape.c defines the Shape operations declared in shape.h. */
#include "shape.h"

/* area returns the area of a Shape. */
double area(struct Shape *s) {
    return PI * s->radius * s->radius;
}

/* label returns a human-readable name for a Shape. */
const char *label(struct Shape *s) {
    return "circle";
}
