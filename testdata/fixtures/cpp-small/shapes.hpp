// shapes.hpp declares the geo namespace: a Shape hierarchy, a Point class,
// a templated helper, and a Color enum class.
#ifndef SHAPES_HPP
#define SHAPES_HPP

namespace geo {

// Color enumerates the supported fill colors. (Two members only: the bundled
// tree-sitter-cpp grammar mis-parses a 3+ enumerator list as an ERROR.)
enum class Color { Red, Green };

// Point is a 2D coordinate with a constructor, data members, and a method.
class Point {
public:
    Point(double x, double y);
    double distanceTo(const Point& other) const;
    static const int dimensions = 2;

private:
    double x_;
    double y_;
};

// Shape is the abstract base of all shapes.
class Shape {
public:
    virtual double area() const = 0;
    virtual ~Shape();
};

// Circle is a concrete Shape with a radius.
class Circle : public Shape {
public:
    Circle(double radius);
    double area() const override;

private:
    double radius_;
};

// scale multiplies a value by a factor (templated helper).
template <typename T>
T scale(T value, T factor) {
    return value * factor;
}

} // namespace geo

#endif // SHAPES_HPP
