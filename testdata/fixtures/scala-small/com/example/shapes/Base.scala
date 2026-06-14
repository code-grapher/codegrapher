package com.example.shapes

abstract class Base(val name: String) extends Shape {
  override def label(): String = name
}
