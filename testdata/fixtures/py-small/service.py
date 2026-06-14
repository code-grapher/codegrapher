"""Service layer exercising cross-module imports and inferred calls."""

from models import Dog

MAX_DOGS = 10


def make_dog(name):
    return Dog(name)


def describe(name):
    d = Dog(name)
    sound = d.speak()
    return d.label, sound
