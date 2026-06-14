import 'dart:math';

abstract class Shape {
  double area();

  String label();
}

mixin Logger {
  void log(String message) {}
}

abstract class Base extends Shape {
  final String name;

  Base(this.name);

  @override
  String label() => name;
}

class Circle extends Base with Logger implements Shape {
  static const double pi = 3.14159;

  final double radius;

  Circle(String name, this.radius) : super(name);

  Circle.unit() : radius = 1.0, super('unit');

  @override
  double area() {
    log('area');
    return pi * radius * radius;
  }
}

enum Size { small, medium, large }
