// widget.h is a .h header whose C++ content (namespace + class) exercises the
// content-sniff that routes ambiguous .h files to the C++ walker.
#ifndef WIDGET_H
#define WIDGET_H

namespace ui {

// Widget is a named UI element.
class Widget {
public:
    Widget(int id);
    int id() const;

private:
    int id_;
};

} // namespace ui

#endif // WIDGET_H
