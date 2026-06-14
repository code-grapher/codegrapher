"""Domain models for the py-small fixture."""

DEFAULT_SOUND = "..."


class Animal:
    """Base animal."""

    def __init__(self, name):
        self.name = name

    def speak(self):
        return DEFAULT_SOUND


class Dog(Animal):
    """A dog."""

    def __init__(self, name):
        self.name = name
        self.legs = 4

    @property
    def label(self):
        return self.name

    def speak(self):
        return "woof"
